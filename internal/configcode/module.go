package configcode

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Module registers /v1/config/* on api nodes (P3.6).
type Module struct {
	Log *slog.Logger
	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "configcode" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	svc := &Service{Log: log}
	if st, ok := app.Get[*settings.Service](a); ok {
		svc.Settings = st
		svc.Env = st
	}
	if wh, ok := app.Get[*webhook.Service](a); ok {
		svc.Webhooks = wh
	}
	if rt, ok := app.Get[*rules.Repo](a); ok {
		svc.Routes = rt
	}
	if ak, ok := app.Get[*apikeys.Service](a); ok {
		svc.APIKeys = ak
	}
	if j, ok := app.Get[*jobs.Service](a); ok {
		svc.Jobs = j
	}
	if ts, ok := app.Get[*tenancy.Service](a); ok {
		svc.Orgs = ts
	}
	if aud, ok := app.Get[*audit.Writer](a); ok {
		svc.Audit = aud
	}
	svc.RegisterHandlers()
	m.svc = svc
	app.Provide(a, svc)
	m.registerAPI(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }
