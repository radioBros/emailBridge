package health

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires account health diagnosis on API nodes.
type Module struct {
	Accounts AccountLoader
	Pool     *ondemand.Pool
	Log      *slog.Logger

	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "health" }

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
	pool := m.Pool
	if pool == nil {
		if p, ok := app.Get[*ondemand.Pool](a); ok {
			pool = p
		}
	}
	loader := m.Accounts
	svc := &Service{DB: db, Accounts: loader, Pool: pool, Log: log}
	m.svc = svc
	app.Provide(a, svc)
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	server, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	m.registerAPI(server)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }
