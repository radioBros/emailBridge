package gmail

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the Gmail Pub/Sub callback and provides Client/Sender/Repo.
type Module struct {
	CallbackURL string // EB_CALLBACK_URL
	BaseURL     string // empty = Google; tests/simulator override
	TokenURL    string
	OIDCToken   string // when set, StaticVerifier (tests); production uses GoogleVerifier
	// PushServiceAccount, when set, is the only service account whose push tokens are accepted.
	PushServiceAccount string
	// Env is EB_ENV; production refuses push when PushServiceAccount is empty (W38).
	Env    string
	Lookup FindByEmail
	Nudge  NudgeFunc
	Log    *slog.Logger

	client *Client
	sender *Sender
	repo   *StateRepo
	push   *PushHandler
}

// Name implements app.Module.
func (m *Module) Name() string { return "providers.gmail" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role {
	return []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}
}

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	log := m.Log
	if log == nil {
		log = a.Log
	}
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
	app.Provide(a, &Backend{Client: m.client, Repo: m.repo, Log: log})

	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		aud := strings.TrimRight(m.CallbackURL, "/") + "/callbacks/gmail"
		h := &PushHandler{
			Audience:           aud,
			Lookup:             m.Lookup,
			Nudge:              m.Nudge,
			Log:                log,
			RequirePushSA:      strings.EqualFold(m.Env, "production"),
			PushServiceAccount: m.PushServiceAccount,
		}
		if m.OIDCToken != "" {
			h.Verify = StaticVerifier{Token: m.OIDCToken}
			h.RequirePushSA = false // test/static path keeps today's behaviour
		} else {
			h.Verify = &GoogleVerifier{ServiceAccount: m.PushServiceAccount}
		}
		m.push = h
		server.Router.Post("/callbacks/gmail", h.ServeHTTP)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Client returns the Gmail REST client.
func (m *Module) Client() *Client { return m.client }

// Sender returns the Gmail send adapter.
func (m *Module) Sender() *Sender { return m.sender }

// SetSenderTokens sets the token source on the live Sender (wiring hotspot).
func (m *Module) SetSenderTokens(ts TokenSource) {
	if m != nil && m.sender != nil {
		m.sender.SetTokens(ts)
	}
}

// Push returns the push handler (tests).
func (m *Module) Push() *PushHandler { return m.push }

// NewRunner builds a Runner for an account (sync seam).
func (m *Module) NewRunner(tokens TokenSource, sink DraftSink) *Runner {
	return &Runner{
		Client: m.client, Tokens: tokens, Sink: sink, Repo: m.repo, Log: m.Log,
	}
}
