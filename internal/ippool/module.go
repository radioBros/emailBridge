package ippool

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/observability"
)

// Module provides a process-wide IP pool Selector (W57).
type Module struct {
	Log *slog.Logger
}

// Name implements app.Module.
func (m *Module) Name() string { return "ippool" }

// Roles implements app.Module — every role that dials mail.
func (m *Module) Roles() []app.Role {
	return []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}
}

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	log := m.Log
	if log == nil {
		log = a.Log
	}
	picks := NewPicksCounter()
	if mtr, ok := app.Get[*observability.Metrics](a); ok && mtr != nil {
		_ = mtr.Register(picks)
	}
	sel := NewSelector(log, picks)
	app.Provide(a, sel)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }
