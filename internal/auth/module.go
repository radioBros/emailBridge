package auth

import (
	"context"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module builds the auth Service on api nodes: it needs *store.DB, *bus.Bus and *httpapi.Server from the
// registry, mounts the routes and middleware, and provides *auth.Service.
type Module struct {
	Options Options
	service *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "auth" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
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
	s, err := New(ctx, m.Options, db.DB, b)
	if err != nil {
		return err
	}
	if mtr, ok := app.Get[*observability.Metrics](a); ok {
		s.Failures = mtr
	}
	if c, ok := app.Get[*crypto.Cipher](a); ok {
		s.Cipher = c
	}
	if aud, ok := app.Get[*audit.Writer](a); ok {
		s.Audit = aud
	}
	if d, ok := app.Get[*egress.Dialer](a); ok {
		s.Egress = d
	}
	s.Mount(srv)
	m.service = s
	app.Provide(a, s)
	return nil
}

// Stop implements app.Module (Limen holds no resources of its own).
func (m *Module) Stop(context.Context) error { return nil }
