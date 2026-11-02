package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/notify"
	"github.com/radiobros/emailbridge/internal/observability"
)

func notifyTestBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "t", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

// TestNotifySourcesQueues: every work stream is reported; a work-queue stream's depth and oldest-message age
// come from its first message, and a paused queue keeps the time it was first seen paused.
func TestNotifySourcesQueues(t *testing.T) {
	b := notifyTestBus(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now()
	src := &notifySources{bus: b, log: quiet, now: func() time.Time { return now }, paused: map[string]time.Time{}, nodes: map[string]bus.NodeInfo{}}
	if _, err := b.Publish(ctx, bus.SysmailSubject, []byte(`{}`), "m1", nil); err != nil {
		t.Fatal(err)
	}
	b.SetQueuePaused(bus.StreamSysmail, true)
	qs := src.queues(ctx)
	if len(qs) != len(bus.WorkStreams()) {
		t.Fatalf("queues = %+v", qs)
	}
	var sys notify.QueueStat
	for _, q := range qs {
		if q.Name == bus.StreamSysmail {
			sys = q
		}
	}
	if sys.Pending != 1 || sys.OldestAge < 0 || sys.PausedAt == nil || !sys.PausedAt.Equal(now) {
		t.Fatalf("sysmail stat = %+v", sys)
	}
	now = now.Add(time.Hour)
	sys = src.queues(ctx)[3]
	if sys.PausedAt == nil || sys.OldestAge < 59*time.Minute || now.Sub(*sys.PausedAt) != time.Hour {
		t.Fatalf("paused since must be kept, age must grow: %+v", sys)
	}
	b.SetQueuePaused(bus.StreamSysmail, false)
	if q := src.queues(ctx)[3]; q.PausedAt != nil {
		t.Fatalf("resumed queue still paused: %+v", q)
	}
	if (&notifySources{}).queues(ctx) != nil || (&notifySources{}).clusterNodes(ctx) != nil {
		t.Fatal("no bus: nil")
	}
}

// TestNotifySourcesClusterNodes: a node that left the registry is still reported with its last sighting
// (so the watcher can raise node-missing); a draining node that left, or one gone for a day, is forgotten.
func TestNotifySourcesClusterNodes(t *testing.T) {
	b := notifyTestBus(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now().UTC()
	src := &notifySources{bus: b, log: quiet, now: func() time.Time { return now }, paused: map[string]time.Time{}, nodes: map[string]bus.NodeInfo{}}
	for _, n := range []bus.NodeInfo{{NodeID: "n1"}, {NodeID: "n2", Draining: true}} {
		if err := b.RegisterNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if got := src.clusterNodes(ctx); len(got) != 2 {
		t.Fatalf("live = %+v", got)
	}
	for _, id := range []string{"n1", "n2"} {
		if err := b.DeregisterNode(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	got := src.clusterNodes(ctx)
	if len(got) != 1 || got[0].ID != "n1" || got[0].LastSeen.IsZero() {
		t.Fatalf("missing node = %+v", got)
	}
	now = now.Add(25 * time.Hour)
	if got := src.clusterNodes(ctx); len(got) != 0 {
		t.Fatalf("forgotten after a day: %+v", got)
	}
}

// TestNotifyWiringHooks: the source hook sets the module's hooks; the metrics hook registers the gauge once
// the module has started and is a no-op without a registry or metrics.
func TestNotifyWiringHooks(t *testing.T) {
	b := notifyTestBus(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Log: quiet, Roles: app.RoleSet{}})
	m := &notify.Module{}
	if err := connectNotifySources(m).Start(context.Background(), a); err != nil || m.BusInfo != nil {
		t.Fatal("no bus: hooks stay nil")
	}
	app.Provide(a, b)
	if err := connectNotifySources(m).Start(context.Background(), a); err != nil || m.BusInfo == nil || m.ClusterNodes == nil {
		t.Fatalf("hooks not set: %v", err)
	}
	if err := registerNotifyMetrics(m).Start(context.Background(), a); err != nil {
		t.Fatal(err) // no observability metrics provided
	}
	app.Provide(a, observability.NewMetrics())
	if err := registerNotifyMetrics(m).Start(context.Background(), a); err != nil {
		t.Fatal(err) // module not started: no collector yet
	}
}

// TestCaptureErrorWithoutNotify: the reporter hooks are safe before the notify module starts.
func TestCaptureErrorWithoutNotify(t *testing.T) {
	(&reporterRef{}).captureError(context.Background(), errors.New("boom"))
	notify.Default().CapturePanic(context.Background(), "boom", nil)
}
