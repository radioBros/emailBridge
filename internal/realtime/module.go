package realtime

import (
	"context"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// BaseTopics are the P0 topics (S13 §6); later phases register theirs from their own modules.
var BaseTopics = []Topic{
	{Name: "dashboard.counters", Permission: tenancy.PermRead, ReadScope: "accounts"},
	{Name: "accounts.state", Permission: tenancy.PermRead, ReadScope: "accounts"},
	{Name: "account.events", Permission: tenancy.PermRead, ReadScope: "events", Params: KeyFilter("accountId", true)},
	{Name: "outbox.updates", Permission: tenancy.PermRead, ReadScope: "outbound"},
	{Name: "logs.tail", Permission: tenancy.PermLogsRead},
}

// Module provides the *Hub and mounts GET /v1/admin/ws (role api).
type Module struct {
	hub        *Hub
	stopEvents func()
}

// Name implements app.Module.
func (m *Module) Name() string { return "realtime" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	o := srv.Options()
	origins := append([]string{o.PublicURL}, o.CORSOrigins...)
	m.hub = NewHub(b, a.Log, origins)
	for _, t := range BaseTopics {
		if err := m.hub.Register(t); err != nil {
			return err
		}
	}
	if err := m.hub.Start(); err != nil {
		return err
	}
	srv.Router.Get("/v1/admin/ws", m.hub.ServeHTTP)
	stop, err := m.hub.FanInEvents(context.WithoutCancel(ctx), b)
	if err != nil {
		a.Log.Warn("live events unavailable in the admin UI; screens still refresh on demand", "err", err)
	} else {
		m.stopEvents = stop
	}
	app.Provide(a, m.hub)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.stopEvents != nil {
		m.stopEvents()
	}
	if m.hub != nil {
		m.hub.Stop()
	}
	return nil
}
