package exports

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires bulk export API, job handler and retention.
type Module struct {
	SignKey   []byte
	PublicURL string
	SignedTTL time.Duration
	Env       string
	Log       *slog.Logger

	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "exports" }

// Roles implements app.Module (handlers on api+scheduler; API on api).
func (m *Module) Roles() []app.Role { return nil }

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
	ttl := m.SignedTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	svc := &Service{
		Repo: &Repo{DB: db}, Env: m.Env, SignKey: m.SignKey, PublicURL: m.PublicURL,
		SignedTTL: ttl, Log: log, Registry: features.DefaultRegistry(),
	}
	if j, ok := app.Get[*jobs.Service](a); ok {
		svc.Jobs = j
	}
	if o, ok := app.Get[*objstore.Store](a); ok {
		svc.Objects = o
	}
	if c, ok := app.Get[*crypto.Cipher](a); ok {
		svc.Cipher = c
	}
	if w, ok := app.Get[*events.Writer](a); ok {
		svc.Events = w
	}
	if au, ok := app.Get[*audit.Writer](a); ok {
		svc.Audit = au
	}
	if st, ok := app.Get[*settings.Service](a); ok {
		svc.Settings = st
	}
	svc.RegisterHandlers()
	m.svc = svc
	app.Provide(a, svc)
	accounts.RegisterEraseHook("exports", func(ctx context.Context, orgID, accountID string) error {
		_, err := svc.EraseAccount(ctx, orgID, accountID)
		return err
	})

	if sched, ok := app.Get[*scheduler.Scheduler](a); ok {
		sched.Add("exports.retention", svc.RetentionDuty)
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

// Migration creates exports indexes. Claimed number: 28 (wire in modules.go).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "exports indexes", Up: EnsureIndexes}
}
