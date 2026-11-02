package idempotency

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module adds the middleware to the API server. Start it after the tenancy module, whose middleware
// resolves the caller this one scopes keys by.
type Module struct {
	TTL   time.Duration
	Stale time.Duration
	Log   *slog.Logger
}

// Name implements app.Module.
func (m *Module) Name() string { return "idempotency" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	mw := &Middleware{DB: db, TTL: m.TTL, Stale: m.Stale, Log: m.Log}
	srv.AddMiddleware(mw.Handler)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }
