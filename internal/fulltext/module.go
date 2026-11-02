package fulltext

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module runs full-text search (G3). On every node it provides *Service and registers the purge duty (run by
// the scheduler leader). On api nodes — the only ones with messages.Service — it also serves the API, handles
// reindex jobs and runs the indexer: one durable consumer per event partition, spread over the api nodes by
// partition leases `fts.<p>`.
type Module struct {
	Env             string
	Registry        *features.Registry
	EventPartitions int           // EB_EVENT_PARTITIONS: must match the event writer's partition count
	LeaseTTL        time.Duration // partition lease TTL
	Log             *slog.Logger

	svc    *Service
	cancel context.CancelFunc
	wg     sync.WaitGroup
	unsub  func() error
}

// Name implements app.Module.
func (m *Module) Name() string { return "fulltext" }

// Roles implements app.Module (every node: the purge duty may run anywhere; the rest checks for api).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.svc = &Service{
		DB: db, Repo: &Repo{DB: db}, Log: log,
		Switches: &Switches{DB: db, Registry: m.Registry, Env: m.Env},
	}
	if w, ok := app.Get[*audit.Writer](a); ok {
		m.svc.Audit = w
	}
	app.Provide(a, m.svc)
	if sched, ok := app.Get[*scheduler.Scheduler](a); ok && sched != nil {
		sched.Add("fulltext.purge", m.svc.PurgeDuty)
	}
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	msgs, err := app.MustGet[*messages.Service](a)
	if err != nil {
		return err
	}
	m.svc.Fetcher = MessagesFetcher{Svc: msgs}
	js, err := app.MustGet[*jobs.Service](a)
	if err != nil {
		return err
	}
	m.svc.Jobs = js
	js.Handle(JobKind, m.svc.handleReindex)
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	m.svc.registerAPI(srv)

	b, ok := app.Get[*bus.Bus](a)
	if !ok || b == nil {
		return nil
	}
	// A switch changed anywhere: forget the cached answers on this node at once instead of after the TTL.
	if m.unsub, err = b.SubscribeCore(bus.SettingsChangedSubject, func(string, []byte) { m.svc.Switches.Clear() }); err != nil {
		return err
	}
	n := m.EventPartitions
	if n <= 0 {
		n = 64
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	bal := &bus.Balancer{
		Leases: b, Prefix: "fts", Roles: []string{string(app.RoleAPI)}, N: n, NodeID: a.NodeID,
		TTL: m.LeaseTTL, Log: log, Work: m.svc.PartitionWork(b),
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		bal.Run(runCtx)
	}()
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	if m.unsub != nil {
		_ = m.unsub()
	}
	return nil
}

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }
