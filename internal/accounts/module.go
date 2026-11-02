package accounts

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging/query"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the accounts API on api nodes.
type Module struct {
	QuotaAccounts int
	IMAPID        map[string]string // global EB_IMAP_ID_* map; verify sends it merged with the account's (W57)
	Registry      *features.Registry
	Log           *slog.Logger
	svc           *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "accounts" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

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
	ev, err := app.MustGet[*events.Writer](a)
	if err != nil {
		return err
	}
	js, err := app.MustGet[*jobs.Service](a)
	if err != nil {
		return err
	}
	b, _ := app.Get[*bus.Bus](a)
	aud, _ := app.Get[*audit.Writer](a)
	dial, _ := app.Get[*egress.Dialer](a)
	objs, _ := app.Get[*objstore.Store](a)
	logs, _ := app.Get[*query.Service](a)
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	reg := m.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	m.svc = &Service{
		DB: db, Cipher: cipher, Events: ev, Jobs: js, Bus: b, Audit: aud,
		Egress: dial, Objects: objs, Logs: logs, Registry: reg,
		QuotaAccounts: m.QuotaAccounts, IMAPID: m.IMAPID, Log: log,
	}
	m.svc.RegisterHandlers()
	app.Provide(a, m.svc)
	m.registerAPI(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration creates accounts indexes (S1).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "accounts indexes", Up: EnsureIndexes}
}
