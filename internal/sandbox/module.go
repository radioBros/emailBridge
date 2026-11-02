package sandbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the sandbox gate, API and retention duty.
type Module struct {
	// Settings resolves rewriteTo and (with Registry) feature switches; optional in tests.
	Settings *settings.Service
	Registry *features.Registry
	// Env is EB_ENV.
	Env string
	// Retention is EB_RETENTION_SANDBOX.
	Retention time.Duration
	// RetentionEvery is the scheduler sweep period (default 1h).
	RetentionEvery time.Duration
	Log            *slog.Logger

	gate    *Gate
	service *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "sandbox" }

// Roles implements app.Module (API registration on api; gate used by submit on every node).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	objects, err := app.MustGet[*objstore.Store](a)
	if err != nil {
		return err
	}
	eventStore, err := app.MustGet[*events.Writer](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	if m.Settings == nil {
		if s, ok := app.Get[*settings.Service](a); ok {
			m.Settings = s
		}
	}
	m.gate = &Gate{
		DB: db, Objects: objects, Events: eventStore, Settings: m.Settings,
		Registry: m.Registry, Env: m.Env, Retention: m.Retention, Log: log,
	}
	repo := &Repo{DB: db}
	m.service = &Service{Repo: repo, Objects: objects, Log: log, RetentionEvery: m.RetentionEvery}
	app.Provide(a, m.gate)
	app.Provide(a, m.service)

	if sched, ok := app.Get[*scheduler.Scheduler](a); ok && sched != nil {
		sched.Add("sandbox.retention", m.service.RetentionDuty)
	}
	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
	}
	_ = ctx
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Gate returns the started sandbox gate (nil before Start). Wire to submit.Module.Sandbox.
func (m *Module) Gate() *Gate { return m.gate }

// Service returns the started API service (nil before Start).
func (m *Module) Service() *Service { return m.service }

var _ Objects = (*objstore.Store)(nil)
