package throttle

import (
	"context"
	"errors"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module provides a Gate on submit (+api) nodes. No migration / HTTP API.
type Module struct {
	Config   ConfigFunc
	Registry *features.Registry
	Env      string
	Log      *slog.Logger

	gate *Gate
}

// Name implements app.Module.
func (m *Module) Name() string { return "throttle" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleSubmit} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	var counter Counter
	if b, ok := app.Get[*bus.Bus](a); ok && b != nil {
		counter = BusCounter{
			AddFn: b.CacheAdd,
			GetFn: func(ctx context.Context, key string) (int64, error) {
				e, err := b.CacheGet(ctx, key)
				if err != nil {
					if errors.Is(err, bus.ErrCacheMiss) {
						return 0, nil
					}
					return 0, err
				}
				return ParseIntBytes(e.Value)
			},
		}
	} else {
		counter = NewMemoryCounter()
	}
	ps, err := rules.NewPreSendEngine(rules.EngineOptions{})
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	cfgFn := m.Config
	if cfgFn == nil {
		db, _ := app.Get[*store.DB](a)
		cfgFn = SettingsConfig(db, m.Registry, m.Env)
	}
	m.gate = &Gate{
		Counter: counter, PreSend: ps, Config: cfgFn,
		Registry: m.Registry, Env: m.Env, Log: log,
	}
	app.Provide(a, m.gate)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Gate returns the started gate (nil before Start).
func (m *Module) Gate() *Gate { return m.gate }

// Migration: none — say so in HOTSPOTS (config in settings, state in eb-cache).
