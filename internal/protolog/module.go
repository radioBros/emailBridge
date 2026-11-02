package protolog

import (
	"context"
	"errors"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging/query"
)

// Module wires the protocol-logs API on api nodes (P1.16).
// Accounts (and optionally Audit) must be set before Start — this package cannot import accounts or
// audit without an import cycle through mailproto/smtpc.
type Module struct {
	Accounts AccountGate
	Audit    DownloadAuditor
	Log      *slog.Logger
	api      *API
}

// Name implements app.Module.
func (m *Module) Name() string { return "protolog" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	if m.Accounts == nil {
		return errors.New("protolog: Accounts gate is required")
	}
	logs, err := app.MustGet[*query.Service](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.api = &API{Accounts: m.Accounts, Logs: logs, Audit: m.Audit, Log: log}
	m.api.Register(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// API returns the started API (nil before Start).
func (m *Module) API() *API { return m.api }
