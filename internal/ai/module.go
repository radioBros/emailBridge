package ai

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Module wires the AI plugin API and helpers.
type Module struct {
	Env      string
	Log      *slog.Logger
	Registry *features.Registry
	svc      *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "ai" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return nil }

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
	log := m.Log
	if log == nil {
		log = a.Log
	}
	_ = log
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: cipher, Env: m.Env, Registry: m.Registry, DB: db,
	}
	if d, ok := app.Get[*egress.Dialer](a); ok {
		svc.Egress = d
	}
	if st, ok := app.Get[*settings.Service](a); ok {
		svc.Settings = st
	}
	if msg, ok := app.Get[*messages.Service](a); ok && msg != nil {
		svc.Messages = MessageTextFrom{Svc: msg}
	}
	if acc, ok := app.Get[*accounts.Service](a); ok && acc != nil {
		svc.Accounts = AccountSwitchesFrom{Svc: acc}
	}
	if aud, ok := app.Get[*audit.Writer](a); ok && aud != nil {
		svc.Audit = func(ctx context.Context, action, accountID, messageID string) {
			_ = aud.Record(ctx, action, audit.Target{Type: "message", ID: messageID}, audit.Changes{
				"accountId": {After: accountID},
			})
		}
	}
	if m.Registry == nil {
		svc.Registry = features.DefaultRegistry()
	}
	m.svc = svc
	app.Provide(a, svc)
	if wh, ok := app.Get[*webhook.Service](a); ok && wh != nil {
		wh.AIEnricher = svc.EnrichMessageReceived
	}
	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		svc.Register(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

// Migration creates aiConfigs indexes. Claimed number: 29 (wire in modules.go).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "aiConfigs indexes", Up: EnsureIndexes}
}
