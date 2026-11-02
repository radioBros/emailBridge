package deliverytest

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

// Module wires delivery-test API and the deliveryTest job handler.
type Module struct {
	Env           string
	TestRecipient string
	Log           *slog.Logger
	svc           *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "deliverytest" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleSubmit} }

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
	_ = log
	repo := &Repo{DB: db}
	svc := &Service{Repo: repo, Env: m.Env, TestRecipient: m.TestRecipient}
	if j, ok := app.Get[*jobs.Service](a); ok {
		svc.Jobs = j
	}
	if sub, ok := app.Get[*submit.Service](a); ok {
		svc.Submit = sub
	}
	svc.RegisterHandlers()
	m.svc = svc
	app.Provide(a, svc)
	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

// Migration is provided by gateways.Migration (smtpGateways + deliveryTests).
