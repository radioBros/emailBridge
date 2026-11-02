package query

import (
	"context"
	"errors"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

// Module registers the /v1/logs API on api nodes. It needs *httpapi.Server (httpapi module) and
// *store.Node (store module) or *bus.Bus, and provides *Service (for the account-erasure job).
type Module struct {
	// Options configure the Service; Node, Bus, Local and Log default from the app.
	Options Options
	// System is the node's log system (settings operations; required).
	System *logging.System
	// Authorizer identifies callers (default TenantAuthorizer until RBAC, P0.14).
	Authorizer Authorizer
	svc        *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "logging.query" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	if m.System == nil {
		return errors.New("logquery: module needs the log system")
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	o := m.Options
	if o.Node == "" {
		o.Node = a.NodeID
	}
	if o.Bus == nil {
		if b, ok := app.Get[*bus.Bus](a); ok {
			o.Bus = b
		}
	}
	if o.Local == nil {
		if n, ok := app.Get[*store.Node](a); ok {
			o.Local = n
		}
	}
	if o.Log == nil {
		o.Log = m.System.For(logging.CatSystem, "logging.query")
	}
	svc, err := New(o)
	if err != nil {
		return err
	}
	m.svc = svc
	Register(srv, svc, APIOptions{System: m.System, Authorizer: m.Authorizer, Log: o.Log})
	app.Provide(a, svc)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }
