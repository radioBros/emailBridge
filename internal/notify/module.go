package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Module wires the notification engine, API, watchers and realtime topic.
type Module struct {
	Env       string
	PublicURL string
	Registry  *features.Registry
	Log       *slog.Logger

	// Optional wiring hooks (set from cmd before Start, or discovered from app).
	BusInfo      func(ctx context.Context) []QueueStat
	ClusterNodes func(ctx context.Context) []NodeStat

	engine   *Engine
	svc      *Service
	watchers *Watchers
	metrics  *Metrics
}

// Name implements app.Module.
func (m *Module) Name() string { return "notify" }

// Roles implements app.Module (API + scheduler).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	repo := &Repo{DB: db}
	prefs := &Prefs{DB: db, Registry: m.Registry, Env: m.Env}
	ch := &Channels{Repo: repo, Prefs: prefs, Log: log, PublicURL: m.PublicURL}
	if hub, ok := app.Get[*realtime.Hub](a); ok {
		ch.Hub = hub
		if err := hub.Register(realtime.Topic{
			Name: "notifications", Permission: tenancy.PermRead, ReadScope: "notifications", Visible: VisibleTo,
		}); err != nil {
			return err
		}
	}
	if w, ok := app.Get[*events.Writer](a); ok {
		ch.Events = w
	}
	if mail, ok := app.Get[*sysmail.Service](a); ok {
		ch.Mail = mail
	}
	engine := NewEngine(repo, ch, log)
	engine.Start()
	m.engine = engine
	defaultEngine.Store(engine)

	svc := &Service{Repo: repo, Engine: engine, Prefs: prefs}
	if au, ok := app.Get[*audit.Writer](a); ok {
		svc.Audit = au
	}
	m.svc = svc
	app.Provide(a, engine)
	app.Provide(a, svc)

	m.metrics = NewMetrics()
	m.watchers = &Watchers{
		Engine: engine, Repo: repo, DB: db, Log: log,
		BusInfo: m.BusInfo, ClusterNodes: m.ClusterNodes,
	}

	if sched, ok := app.Get[*scheduler.Scheduler](a); ok {
		sched.Add("notify.watchers", m.watchers.Duty)
		sched.Add("notify.digest", m.digestDuty)
		sched.Add("notify.metrics", m.metricsDuty)
	}

	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.engine != nil {
		m.engine.Stop()
	}
	defaultEngine.CompareAndSwap(m.engine, nil)
	return nil
}

// Engine returns the started engine.
func (m *Module) Engine() *Engine { return m.engine }

// Service returns the HTTP service.
func (m *Module) Service() *Service { return m.svc }

// Metrics returns the metrics object (register Collector() from wiring).
func (m *Module) Metrics() *Metrics { return m.metrics }

func (m *Module) metricsDuty(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if m.metrics != nil && m.engine != nil {
			m.metrics.Refresh(ctx, m.engine.Repo)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// digestDuty sends due email digests (W75 §6.2) on the watcher cadence.
func (m *Module) digestDuty(ctx context.Context) {
	if m.engine == nil {
		return
	}
	db := m.engine.Repo.DB
	every(ctx, func(ctx context.Context) time.Duration { return watcherInterval(ctx, db) }, m.flushDigests)
}

func (m *Module) flushDigests(ctx context.Context) {
	if m.engine == nil {
		return
	}
	m.engine.Channels.FlushDigests(ctx)
}
