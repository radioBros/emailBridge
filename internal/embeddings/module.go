package embeddings

import (
	"context"
	"log/slog"
	"sync"

	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module runs semantic search (G1). It starts after the full-text and AI modules (it needs both services).
// On every node it provides *Service, hooks the AI settings' model-change callback and registers the sweep
// duty (run by the scheduler leader). On api nodes — where the full-text indexer runs — it also registers as
// the indexer's observer, runs the embed workers, serves the API and handles reindex jobs.
type Module struct {
	Env      string
	Registry *features.Registry
	Log      *slog.Logger
	// MaxChunks caps the chunks compared per query (default 50 000).
	MaxChunks int
	// RequestsPerMinute limits background embedding requests per organisation and node (default 60).
	RequestsPerMinute float64

	svc    *Service
	cancel context.CancelFunc
	wg     sync.WaitGroup
	unsub  func() error
}

// Name implements app.Module.
func (m *Module) Name() string { return "embeddings" }

// Roles implements app.Module (every node: the sweep may run anywhere; the rest checks for api).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	fts, err := app.MustGet[*fulltext.Service](a)
	if err != nil {
		return err
	}
	aiSvc, err := app.MustGet[*ai.Service](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.svc = &Service{
		DB: db, Repo: &Repo{DB: db}, AI: aiSvc, Log: log,
		Switches:  &fulltext.Switches{DB: db, Registry: m.Registry, Env: m.Env, Name: SwitchName},
		FullText:  fts.Switches,
		MaxChunks: m.MaxChunks, RequestsPerMinute: m.RequestsPerMinute,
	}
	if w, ok := app.Get[*audit.Writer](a); ok {
		m.svc.Audit = w
	}
	aiSvc.EmbeddingsChanged = m.svc.ModelChanged
	app.Provide(a, m.svc)
	if sched, ok := app.Get[*scheduler.Scheduler](a); ok && sched != nil {
		sched.Add("embeddings.purge", m.svc.SweepDuty)
	}
	if b, ok := app.Get[*bus.Bus](a); ok && b != nil {
		// A switch changed anywhere: forget the cached answers on this node at once instead of after the TTL.
		if m.unsub, err = b.SubscribeCore(bus.SettingsChangedSubject, func(string, []byte) { m.svc.Switches.Clear() }); err != nil {
			return err
		}
	}
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
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
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.svc.RunWorkers(runCtx)
	}()
	fts.AddObserver(m.svc)
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
