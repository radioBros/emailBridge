package templates

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires template CRUD and preview on api nodes.
type Module struct {
	Settings *settings.Service
	Registry *features.Registry
	Env      string
	Log      *slog.Logger
	svc      *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "templates" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

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
	if m.Settings == nil {
		if s, ok := app.Get[*settings.Service](a); ok {
			m.Settings = s
		}
	}
	m.svc = &Service{
		Repo: &Repo{DB: db}, Settings: m.Settings, Registry: m.Registry, Env: m.Env, Log: log,
	}
	app.Provide(a, m.svc)
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
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration creates templates indexes (brief: migration 27).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "templates indexes", Up: EnsureIndexes}
}
