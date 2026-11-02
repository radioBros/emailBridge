package sysmail

import (
	"context"
	"log/slog"
	"strings"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires system mail on every node: enqueue is available wherever the service is provided;
// the worker runs on every role so any node can drain EMAILBRIDGE_SYSMAIL.
//
// Rate limiting is an in-memory token bucket (10/hour per recipient) local to each node for P0 —
// not cluster-wide. A NATS KV counter can replace it later.
type Module struct {
	Options Options
	svc     *Service
	bus     *bus.Bus
	stop    func()
}

// Name implements app.Module.
func (m *Module) Name() string { return "sysmail" }

// Roles implements app.Module — empty means every node.
func (m *Module) Roles() []app.Role { return nil }

// Start provides *Service and starts the worker when mode is smtp, or account with a validated sender.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	o := m.Options
	if o.Log == nil {
		if a != nil && a.Log != nil {
			o.Log = a.Log
		} else {
			o.Log = slog.Default()
		}
	}
	b, _ := app.Get[*bus.Bus](a)
	m.bus = b
	m.svc = New(o, b)
	if db, ok := app.Get[*store.DB](a); ok && db != nil {
		m.svc.Records = &Records{DB: db}
	}
	app.Provide(a, m.svc)
	app.Provide(a, m)
	mode := strings.ToLower(strings.TrimSpace(o.Mode))
	switch mode {
	case ModeSMTP:
		return m.ensureWorker(ctx)
	case ModeAccount:
		if m.svc.ActivateAccount(ctx) {
			return m.ensureWorker(ctx)
		}
	}
	return nil
}

// BindAccountSender injects the submit adapter (and optional account probe) after Start — used by
// cmd/ once submit and accounts are ready. Validates the configured account; on failure logs and
// leaves Enqueue returning feature_disabled. Starts the worker when validation succeeds.
func (m *Module) BindAccountSender(ctx context.Context, sender AccountSender, check AccountChecker) {
	if m == nil || m.svc == nil {
		return
	}
	m.svc.SetAccount(sender, check)
	if !strings.EqualFold(strings.TrimSpace(m.svc.o.Mode), ModeAccount) {
		return
	}
	if !m.svc.ActivateAccount(ctx) {
		return
	}
	if err := m.ensureWorker(ctx); err != nil {
		m.svc.log.Error("system mail account worker failed to start", "err", err)
		m.svc.setAccountOK(false)
	}
}

func (m *Module) ensureWorker(ctx context.Context) error {
	if m.bus == nil || m.stop != nil || m.svc == nil {
		return nil
	}
	stop, err := m.svc.StartWorker(ctx, m.bus)
	if err != nil {
		return err
	}
	m.stop = stop
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	return nil
}
