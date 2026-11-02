package oauth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires OAuth apps, callbacks and connect links on API nodes.
type Module struct {
	CallbackURL string
	PublicURL   string
	// GmailAPIRoot / GraphAPIRoot override the provider API hosts (EB_GMAIL_API_URL / EB_GRAPH_API_URL).
	GmailAPIRoot, GraphAPIRoot string
	NodeID                     string
	Log                        *slog.Logger
	svc                        *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "oauth" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit} }

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
	dialer, _ := app.Get[*egress.Dialer](a)
	aud, _ := app.Get[*audit.Writer](a)
	b, _ := app.Get[*bus.Bus](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	repo := &Repo{DB: db}
	svc := &Service{
		Repo: repo, Cipher: cipher, Egress: dialer, Audit: aud,
		CallbackURL: m.CallbackURL, PublicURL: m.PublicURL, NodeID: m.NodeID, Log: log,
		GmailAPIRoot: m.GmailAPIRoot, GraphAPIRoot: m.GraphAPIRoot,
	}
	if b != nil {
		svc.State = b
		svc.Leases = b
	}
	m.svc = svc
	app.Provide(a, repo)
	app.Provide(a, svc)
	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
		m.mountPublic(server)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

func (m *Module) mountPublic(server *httpapi.Server) {
	server.Router.Get("/callbacks/{provider}", func(w http.ResponseWriter, r *http.Request) {
		provider := chi.URLParam(r, "provider")
		switch provider {
		case ProviderGmail, ProviderOutlook, ProviderMailru, ProviderGeneric:
		default:
			http.NotFound(w, r)
			return
		}
		m.svc.HandleCallback(r.Context(), w, r, provider)
	})
	// Redeem is under /v1 so the SPA can own GET /connect/{token} (P6.4 hosted page).
	server.Router.Get("/v1/connect-links/{token}/redeem", func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		if token == "" {
			token = strings.TrimPrefix(r.URL.Path, "/v1/connect-links/")
			token = strings.TrimSuffix(token, "/redeem")
		}
		m.svc.RedeemConnectLink(r.Context(), w, r, token)
	})
}
