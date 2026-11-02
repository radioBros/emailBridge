package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Module wires the MCP streamable HTTP endpoint, its generated API tools and the dedicated listener.
type Module struct {
	Env      string
	Log      *slog.Logger
	Registry *features.Registry
	// ReconcileEvery re-reads mcp.listen as a fallback to the settings-changed signal (default 1 min).
	ReconcileEvery time.Duration
	handler        *Handler
	control        *Control
	unsub          func() error
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "mcp" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	gw := &Gateway{}
	if acc, ok := app.Get[*accounts.Service](a); ok {
		gw.Accounts = acc
	}
	if msg, ok := app.Get[*messages.Service](a); ok {
		gw.Messages = msg
	}
	if sub, ok := app.Get[*submit.Service](a); ok {
		gw.Submit = sub
	}
	if wh, ok := app.Get[*webhook.Service](a); ok {
		gw.Deliveries = wh
	}
	if ev, ok := app.Get[*events.Writer](a); ok {
		gw.Events = ev
	}
	if au, ok := app.Get[*audit.Writer](a); ok {
		gw.Audit = au
	}
	if aiSvc, ok := app.Get[*ai.Service](a); ok && aiSvc != nil {
		gw.AI = aiSvc
	}
	var st *settings.Service
	if s, ok := app.Get[*settings.Service](a); ok {
		st = s
	}
	gw.ResolvePII = func(ctx context.Context) (bool, int) {
		return ResolvePIISettings(ctx, st)
	}
	h := &Handler{
		Gateway: gw, Settings: st, Registry: m.Registry, Env: m.Env,
	}
	if h.Registry == nil {
		h.Registry = features.DefaultRegistry()
	}
	m.handler = h
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	h.Mount(srv)

	ctl := &Control{Handler: h, Listener: &Listener{Handler: srv, Log: log}, PublicURL: srv.Options().PublicURL}
	if st != nil {
		ctl.Settings = st
	}
	m.control = ctl
	app.Provide(a, ctl)
	ctl.Register(srv)
	if b, ok := app.Get[*bus.Bus](a); ok && b != nil && st != nil {
		m.unsub, err = b.SubscribeCore(bus.SettingsChangedSubject, func(_ string, data []byte) {
			var ch struct {
				Keys []string `json:"keys"`
			}
			if json.Unmarshal(data, &ch) != nil || !slices.Contains(ch.Keys, SettingListen) {
				return
			}
			// The settings module's own subscriber may not have dropped its cache yet.
			st.Invalidate("global")
			ctl.Reconcile(context.Background())
		})
		if err != nil {
			return err
		}
	}
	every := m.ReconcileEvery
	if every <= 0 {
		every = time.Minute
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		// The first reconcile is Control.Activate, once every route is registered.
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				ctl.Reconcile(runCtx)
			}
		}
	}()
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(ctx context.Context) error {
	if m.unsub != nil {
		_ = m.unsub()
	}
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	if m.control != nil && m.control.Listener != nil {
		m.control.Listener.Stop(ctx)
	}
	return nil
}

// Control returns the listener control (nil before Start).
func (m *Module) Control() *Control { return m.control }

// Handler returns the mounted handler (nil before Start).
func (m *Module) Handler() *Handler { return m.handler }

// Migration is a no-op marker shared with ai (indexes live in ai.Migration).
// Claimed number: 29 — prefer ai.Migration(29) in modules.go.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "mcp marker", Up: func(context.Context, *store.DB) error {
		return nil
	}}
}
