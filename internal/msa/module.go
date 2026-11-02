package msa

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

// Module wires the SMTP submission server and credential API.
type Module struct {
	Options  Options
	Env      string
	Log      *slog.Logger
	Registry *features.Registry
	// Accounts / APIKeys / Gate are optional; set by wiring or left nil in tests.
	Accounts Accounts
	APIKeys  APIKeys
	Gate     FeatureGate

	svc    *Service
	server *Server
}

// Name implements app.Module.
func (m *Module) Name() string { return "msa" }

// Roles implements app.Module — listeners and API on the api role.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	repo := &Repo{DB: db}
	m.svc = &Service{Repo: repo, OwnsAccount: func(ctx context.Context, orgID, accountID string) (bool, error) {
		n, err := db.Collection("accounts").CountDocuments(ctx, bson.M{"_id": accountID, "organizationId": orgID}, options.Count().SetLimit(1))
		return n > 0, err
	}}
	app.Provide(a, m.svc)

	if srv, err := app.MustGet[*httpapi.Server](a); err == nil {
		m.svc.Register(srv)
	}

	var sub Submitter
	if s, ok := app.Get[*submit.Service](a); ok {
		sub = s
	}
	authRate := newLimiter(m.Options.AuthRatePerIP)
	msgRate := newLimiter(m.Options.RatePerAccount)
	be := &Backend{
		Auth: &Authenticator{
			Accounts: m.Accounts, Creds: repo, APIKeys: m.APIKeys, AuthRate: authRate,
		},
		Submit: sub, MsgRate: msgRate, Log: log, Gate: m.gate(),
	}
	m.server = NewServer(m.Options, be, log)
	return m.server.Start()
}

func (m *Module) gate() FeatureGate {
	if m.Gate != nil {
		return m.Gate
	}
	reg := m.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	env := m.Env
	return featureGate{reg: reg, env: env}
}

type featureGate struct {
	reg *features.Registry
	env string
}

func (g featureGate) MSAEnabled(context.Context) bool {
	sw, ok := g.reg.Get("protocol.msa")
	if !ok {
		return false
	}
	v, _ := features.Resolve(sw, g.env, features.Values{})
	return v.Bool != nil && *v.Bool
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.server != nil {
		return m.server.Close()
	}
	return nil
}

// Service returns the credential service.
func (m *Module) Service() *Service { return m.svc }

// Migration creates msaCredentials indexes. Claimed number: 30 (wire in modules.go).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "msa credentials indexes", Up: EnsureIndexes}
}
