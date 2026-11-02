package imapproxy

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the IMAP proxy and credential API.
type Module struct {
	Options  Options
	Env      string
	Log      *slog.Logger
	Registry *features.Registry
	Accounts Accounts
	Source   IMAPSource
	Dial     DialFunc
	Gate     FeatureGate

	svc    *Service
	server *Server
}

// Name implements app.Module.
func (m *Module) Name() string { return "imapproxy" }

// Roles implements app.Module.
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

	h := &connHandler{
		auth: &Authenticator{
			Accounts: m.Accounts, Creds: repo, AuthRate: newLimiter(m.Options.AuthRatePerIP),
		},
		source: m.Source, dial: m.Dial,
		budget: newBudget(m.Options.MaxConnectionsPerAcct),
		log:    log, gate: m.gate(),
	}
	m.server = NewServer(m.Options, h, log)
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
	return featureGate{reg: reg, env: m.Env}
}

type featureGate struct {
	reg *features.Registry
	env string
}

func (g featureGate) IMAPProxyEnabled(context.Context) bool {
	sw, ok := g.reg.Get("protocol.imapProxy")
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

// Migration creates imapProxyCredentials indexes. Claimed number: **30** together with
// msa.EnsureIndexes — wire a single Up that calls both (see HOTSPOTS).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "imap proxy credentials indexes", Up: EnsureIndexes}
}
