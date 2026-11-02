package graph

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module mounts the Graph change-notification callback and provides Client/Sender/Repo.
type Module struct {
	CallbackURL string
	BaseURL     string
	TokenURL    string
	Nudge       Nudger
	Log         *slog.Logger

	client  *Client
	sender  *Sender
	repo    *StateRepo
	handler *CallbackHandler
}

// Name implements app.Module.
func (m *Module) Name() string { return "providers.graph" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role {
	return []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}
}

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	m.client = &Client{BaseURL: m.BaseURL, TokenURL: m.TokenURL}
	if d, ok := app.Get[*egress.Dialer](a); ok && d != nil {
		m.client.HTTP = d.HTTPClient(30 * time.Second)
	}
	m.sender = &Sender{Client: m.client}
	if db, ok := app.Get[*store.DB](a); ok && db != nil {
		m.repo = &StateRepo{DB: db}
		app.Provide(a, m.repo)
	}
	app.Provide(a, m.client)
	app.Provide(a, m.sender)

	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		h := &CallbackHandler{Nudge: m.Nudge, Repo: m.repo}
		m.handler = h
		app.Provide(a, h)
		server.Router.Post("/callbacks/graph", h.ServeHTTP)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Callback returns the live change-notification handler.
func (m *Module) Callback() *CallbackHandler { return m.handler }

// Client returns the Graph REST client.
func (m *Module) Client() *Client { return m.client }

// Sender returns the Graph send adapter.
func (m *Module) Sender() *Sender { return m.sender }

// SetSenderTokens sets the token source on the live Sender (wiring hotspot).
func (m *Module) SetSenderTokens(ts TokenSource) {
	if m != nil && m.sender != nil {
		m.sender.SetTokens(ts)
	}
}

// NewRunner builds a Runner for an account (sync seam).
func (m *Module) NewRunner(tokens TokenSource, sink DraftSink) *Runner {
	return &Runner{Client: m.client, Tokens: tokens, Sink: sink, Repo: m.repo}
}

// NewBackend builds the sync.Backend for Manager.Backends["graph"].
func (m *Module) NewBackend() *Backend {
	return &Backend{Client: m.client, Repo: m.repo, Log: m.Log, Config: Config{CallbackURL: m.CallbackURL}}
}
