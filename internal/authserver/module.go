package authserver

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module provides the credential-server Resolver on API / sync / submit nodes.
type Module struct {
	Log *slog.Logger
	r   *Resolver
}

// Name implements app.Module.
func (m *Module) Name() string { return "authserver" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role {
	return []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}
}

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
	dial, _ := app.Get[*egress.Dialer](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.r = NewResolver(db, cipher, dial, log)
	app.Provide(a, m.r)
	app.Provide(a, Credentials(m.r))
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Resolver returns the started resolver.
func (m *Module) Resolver() *Resolver { return m.r }
