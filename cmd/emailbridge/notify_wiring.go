package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/notify"
	"github.com/radiobros/emailbridge/internal/observability"
)

// nodeMemory is how long a node that left the registry is still reported to the node-missing watcher.
// Afterwards it is forgotten and its problem resolves (a node removed on purpose stops alerting).
const nodeMemory = 24 * time.Hour

// connectNotifySources gives the notification watchers (W75 §4) their read-only data sources: JetStream
// stream / consumer state for the queue watcher and the node registry for the node-missing watcher. It is
// listed before the notify module because the module copies the hooks into its watchers when it starts.
func connectNotifySources(m *notify.Module) app.Module {
	return &hook{name: "wire.notify-sources", start: func(_ context.Context, a *app.App) error {
		b, ok := app.Get[*bus.Bus](a)
		if !ok || b == nil {
			return nil
		}
		src := &notifySources{bus: b, log: a.Log, now: time.Now, paused: map[string]time.Time{}, nodes: map[string]bus.NodeInfo{}}
		m.BusInfo = src.queues
		m.ClusterNodes = src.clusterNodes
		return nil
	}}
}

// registerNotifyMetrics registers the open-problems gauge on the process Prometheus registry. Listed after
// the notify module: its metrics exist only once the module has started.
func registerNotifyMetrics(m *notify.Module) app.Module {
	return &hook{name: "wire.notify-metrics", start: func(_ context.Context, a *app.App) error {
		reg, ok := app.Get[*observability.Metrics](a)
		if !ok || reg == nil {
			return nil
		}
		c := m.Metrics().Collector()
		if c == nil {
			return nil
		}
		if err := reg.Register(c); err != nil {
			var already prometheus.AlreadyRegisteredError
			if !errors.As(err, &already) {
				return err
			}
		}
		return nil
	}}
}

// notifySources keeps the little state the watchers need across ticks (pause start times, last sight of
// each node). It runs on the scheduler leader only, but is safe for concurrent use.
type notifySources struct {
	bus *bus.Bus
	log *slog.Logger
	now func() time.Time

	mu     sync.Mutex
	paused map[string]time.Time    // queue → first tick it was seen paused
	nodes  map[string]bus.NodeInfo // node id → last registry entry seen
}

// queues reports every JetStream work stream: depth and the age of its oldest unprocessed message (see
// bus.StreamBacklog) and how long it has been paused.
func (s *notifySources) queues(ctx context.Context) []notify.QueueStat {
	if s.bus == nil || s.bus.JS == nil {
		return nil
	}
	now := s.now().UTC()
	out := make([]notify.QueueStat, 0, len(bus.WorkStreams()))
	for _, name := range bus.WorkStreams() {
		q, err := s.queue(ctx, name, now)
		if err != nil {
			s.log.Warn("notify queue watcher could not read a stream", "stream", name, "err", err.Error())
			continue
		}
		out = append(out, q)
	}
	return out
}

func (s *notifySources) queue(ctx context.Context, name string, now time.Time) (notify.QueueStat, error) {
	q := notify.QueueStat{Name: name}
	bl, err := s.bus.StreamBacklog(ctx, name)
	if err != nil {
		return q, err
	}
	q.Pending, q.AckPending = bl.Pending, bl.AckPending
	if bl.Pending > 0 && !bl.Oldest.IsZero() {
		q.OldestAge = now.Sub(bl.Oldest)
	}
	q.OldestAge = max(q.OldestAge, 0) // clock skew between the stream and this node
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bus.QueuePaused(name) {
		since, ok := s.paused[name]
		if !ok {
			since = now
			s.paused[name] = since
		}
		q.PausedAt = &since
	} else {
		delete(s.paused, name)
	}
	return q, nil
}

// clusterNodes reports the live registry plus nodes seen earlier that have left it, with the time they were
// last seen, so the watcher can tell a missing node. A node that was draining when it left was stopped on
// purpose and is not reported.
func (s *notifySources) clusterNodes(ctx context.Context) []notify.NodeStat {
	if s.bus == nil {
		return nil
	}
	live, err := s.bus.LiveNodes(ctx)
	if err != nil {
		s.log.Warn("notify node watcher could not read the node registry", "err", err.Error())
		return nil
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	present := make(map[string]bool, len(live))
	out := make([]notify.NodeStat, 0, len(live))
	for _, n := range live {
		present[n.NodeID] = true
		s.nodes[n.NodeID] = n
		out = append(out, notify.NodeStat{ID: n.NodeID, LastSeen: n.SeenAt})
	}
	for id, n := range s.nodes {
		if present[id] {
			continue
		}
		if n.Draining || now.Sub(n.SeenAt) > nodeMemory {
			delete(s.nodes, id)
			continue
		}
		out = append(out, notify.NodeStat{ID: id, LastSeen: n.SeenAt})
	}
	return out
}
