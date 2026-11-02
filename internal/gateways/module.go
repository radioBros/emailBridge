package gateways

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires SMTP/API gateway CRUD and delivery webhooks on API nodes.
type Module struct {
	EHLO        string
	CallbackURL string // EB_CALLBACK_URL → webhookUrl in views + SNS confirm
	Log         *slog.Logger
	Sink        DeliverySink // optional; Claude wires outbound status
	svc         *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "gateways" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleSubmit} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	cipher, err := app.MustGet[*crypto.Cipher](a)
	if err != nil {
		return err
	}
	dialer, _ := app.Get[*egress.Dialer](a)
	aud, _ := app.Get[*audit.Writer](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	_ = log
	repo := &Repo{DB: db}
	m.svc = &Service{
		Repo: repo, Cipher: cipher, Egress: dialer, Audit: aud, EHLO: m.EHLO,
		CallbackBase: m.CallbackURL, Sink: m.Sink, limiter: newCallbackLimiter(60),
	}
	app.Provide(a, repo)
	app.Provide(a, m.svc)
	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
		server.Router.Post("/callbacks/gateways/{gatewayId}", m.rawGatewayWebhook)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }
