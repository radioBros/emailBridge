package apikeys

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Module wires API key CRUD and exposes the KeyResolver on api nodes.
type Module struct {
	Log *slog.Logger
	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "apikeys" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	authSvc, err := app.MustGet[*auth.Service](a)
	if err != nil {
		return err
	}
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	aud, _ := app.Get[*audit.Writer](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.svc = New(db, authSvc, b, aud, log)
	app.Provide(a, m.svc)
	app.Provide(a, tenancy.KeyResolver(m.svc.Resolve))
	m.registerAPI(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration creates apiKeys indexes (S1).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "apiKeys indexes", Up: EnsureIndexes}
}

// GranularScopesMigration is a no-op marker for P3.4a (scopes are free-form strings validated at write).
func GranularScopesMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "apiKeys granular scopes", Up: func(context.Context, *store.DB) error {
		return nil
	}}
}
