package bus

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/store"
)

// EmbeddedLock is the Mongo lock that keeps a second node from running an embedded server against the
// same database (S4 §1).
const EmbeddedLock = "bus.embedded"

// Module opens the bus at startup, provides *Bus and reports readiness. In embedded mode it first takes
// the EmbeddedLock through the *store.DB provided by the store module.
type Module struct {
	Options    Options
	bus        *Bus
	lock       *store.Lock
	cancel     context.CancelFunc
	hbCancel   context.CancelFunc
	hbDone     chan struct{}
	queueUnsub func() error
	drainUnsub func() error
}

// Name implements app.Module.
func (m *Module) Name() string { return "bus" }

// Roles implements app.Module (platform module).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if m.Options.NodeID == "" {
		m.Options.NodeID = a.NodeID
	}
	if m.Options.Log == nil {
		m.Options.Log = a.Log
	}
	if m.Options.Mode == ModeEmbedded || m.Options.Mode == "" {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		lock, err := acquireEmbeddedLock(ctx, db, a)
		if err != nil {
			return err
		}
		m.lock = lock
	}
	b, err := Open(ctx, m.Options)
	if err != nil {
		m.releaseLock(ctx)
		return err
	}
	m.bus = b
	app.Provide(a, b)
	if unsub, err := b.watchQueueControl(); err != nil {
		a.Log.Warn("queue pause fan-out unavailable", "err", err)
	} else {
		m.queueUnsub = unsub
	}
	if unsub, err := b.SubscribeCore(ClusterDrainSubject(a.NodeID), func(string, []byte) {
		_ = b.SetDraining(context.WithoutCancel(ctx), true)
	}); err != nil {
		a.Log.Warn("cluster drain subscription unavailable", "err", err)
	} else {
		m.drainUnsub = unsub
	}
	hctx, hcancel := context.WithCancel(context.WithoutCancel(ctx))
	info := NodeInfo{NodeID: a.NodeID, Roles: roleNames(a.Roles), Version: a.Build.Version, StartedAt: time.Now().UTC()}
	if err := b.RegisterNode(ctx, info); err != nil {
		a.Log.Warn("node registration failed; retrying with the heartbeat", "err", err)
	}
	m.hbDone = make(chan struct{})
	go func() {
		defer close(m.hbDone)
		b.heartbeat(hctx, info, NodeHeartbeat, a.Log)
	}()
	m.hbCancel = hcancel
	if m.lock != nil {
		lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.cancel = cancel
		go func() {
			select {
			case <-m.lock.Lost():
				a.Log.Error("embedded NATS lock lost to another node; this node must stop", "lock", EmbeddedLock)
			case <-lctx.Done():
			}
		}()
	}
	return nil
}

// embeddedLockTTL is how long the embedded lock lives without a refresh.
const embeddedLockTTL = 30 * time.Second

// embeddedLockWait is how long startup waits for a held lock. A node that was killed before its Stop
// released the lock (a container restart past its stop grace period) leaves a lock that expires within
// one TTL, so a replacement waits it out instead of crash-looping; a live second node keeps refreshing
// the lock and is still refused once the wait ends.
var embeddedLockWait = embeddedLockTTL + 5*time.Second

func acquireEmbeddedLock(ctx context.Context, db *store.DB, a *app.App) (*store.Lock, error) {
	deadline := time.Now().Add(embeddedLockWait)
	logged := false
	for {
		lock, err := db.TryLock(ctx, EmbeddedLock, a.NodeID, embeddedLockTTL)
		if !errors.Is(err, store.ErrLockHeld) {
			return lock, err
		}
		if time.Now().After(deadline) {
			return nil, errors.New("bus: another node runs the embedded NATS server against this database; use EB_NATS_MODE=external for more than one node")
		}
		if !logged {
			a.Log.Info("embedded NATS lock held; waiting for it to expire", "lock", EmbeddedLock, "maxWait", embeddedLockWait)
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Module) releaseLock(ctx context.Context) {
	if m.lock != nil {
		_ = m.lock.Release(context.WithoutCancel(ctx))
		m.lock = nil
	}
}

// Stop implements app.Module.
func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.queueUnsub != nil {
		_ = m.queueUnsub()
	}
	if m.drainUnsub != nil {
		_ = m.drainUnsub()
	}
	if m.hbCancel != nil {
		m.hbCancel() // deregisters this node so its partitions move at once
		<-m.hbDone
	}
	if m.bus != nil {
		m.bus.Close()
	}
	m.releaseLock(ctx)
	return nil
}

// Ready implements app.Checker.
func (m *Module) Ready(ctx context.Context) error { return m.bus.Ready(ctx) }

func roleNames(rs app.RoleSet) []string {
	return strings.Split(rs.String(), ",")
}
