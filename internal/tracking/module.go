package tracking

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires public tracking/unsubscribe routes and the prepare service.
type Module struct {
	SignKey   []byte
	PublicURL string
	TTL       time.Duration
	Settings  *settings.Service
	Registry  *features.Registry
	Env       string
	Log       *slog.Logger
	// Unsub is set by wiring to suppression.Service.AddUnsubscribe (mailingListID may be empty).
	Unsub func(ctx context.Context, org, address, reason, mailingListID string) error
	// AccountOf is set by wiring: the account of an outbound message, so tracking events carry accountId.
	AccountOf func(ctx context.Context, org, outboundID string) (accountID string, externalID *string)
	svc       *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "tracking" }

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
	if m.Settings == nil {
		if s, ok := app.Get[*settings.Service](a); ok {
			m.Settings = s
		}
	}
	ev, _ := app.Get[*events.Writer](a)
	m.svc = &Service{
		Repo: &Repo{DB: db}, SignKey: m.SignKey, PublicURL: m.PublicURL, TTL: m.TTL,
		Events: ev, Settings: m.Settings, Registry: m.Registry, Env: m.Env,
		Unsub: m.Unsub, AccountOf: m.AccountOf, Log: log,
	}
	app.Provide(a, m.svc)
	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		srv.Router.Get("/t/o/{token}", func(w http.ResponseWriter, r *http.Request) {
			m.svc.HandleOpen(w, r, chi.URLParam(r, "token"))
		})
		srv.Router.Get("/t/c/{token}", func(w http.ResponseWriter, r *http.Request) {
			m.svc.HandleClick(w, r, chi.URLParam(r, "token"))
		})
		srv.Router.Get("/u/{token}", func(w http.ResponseWriter, r *http.Request) {
			m.svc.HandleUnsubGET(w, r, chi.URLParam(r, "token"))
		})
		srv.Router.Post("/u/{token}", func(w http.ResponseWriter, r *http.Request) {
			m.svc.HandleUnsubPOST(w, r, chi.URLParam(r, "token"))
		})
		m.documentRoutes(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }
