package pecreceipts

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the PEC receipts API on api nodes (S17 §8 / W40).
type Module struct {
	Raw      RawSource
	Enabled  EnabledFn
	Registry *features.Registry
	Env      string
	Log      *slog.Logger
	// Evidence verifies archived evidence (P9.2); set by wiring.
	Evidence EvidenceVerifier
	svc      *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "pecreceipts" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

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
	_ = log
	reg := m.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	m.svc = &Service{
		Repo: &Repo{DB: db}, Raw: m.Raw, Enabled: m.Enabled,
		Registry: reg, Env: m.Env, Evidence: m.Evidence,
	}
	app.Provide(a, m.svc)
	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.svc.register(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }
