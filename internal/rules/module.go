package rules

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module provides the CEL engine on webhook (+api) nodes and registers the routes API on api nodes.
type Module struct {
	EngineOptions  EngineOptions
	EndpointExists EndpointExists
	EventLoader    EventLoader
	EnvLoader      EnvLoader
	AccountLoader  AccountLoader
	Log            *slog.Logger

	engine *Engine
	repo   *Repo
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "rules" }

// Roles implements app.Module — engine on webhook; API registration when api is also present.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleWebhook} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	eng, err := NewEngine(m.EngineOptions)
	if err != nil {
		return err
	}
	m.engine = eng
	app.Provide(a, eng)

	db, _ := app.Get[*store.DB](a)
	cipher, _ := app.Get[*crypto.Cipher](a)
	js, _ := app.Get[*jobs.Service](a)
	aw, _ := app.Get[*audit.Writer](a)
	lists := &ListCache{DB: db}
	stats := NewStatsRecorder(db)
	stats.Log = m.Log
	if mtr, ok := app.Get[*observability.Metrics](a); ok && mtr != nil {
		if err := mtr.Register(stats.Collector()); err != nil {
			var already prometheus.AlreadyRegisteredError
			if !errors.As(err, &already) {
				return err
			}
		}
	}
	m.repo = &Repo{
		DB: db, Engine: eng, Cipher: cipher, Jobs: js, Audit: aw,
		EndpointExists: m.EndpointExists,
		EventLoader:    m.EventLoader,
		EnvLoader:      m.EnvLoader,
		AccountLoader:  m.AccountLoader,
		Lists:          lists,
		Stats:          stats,
	}
	app.Provide(a, m.repo)
	app.Provide(a, lists)
	app.Provide(a, stats)
	m.repo.RegisterHandlers()
	if db != nil {
		runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.cancel = cancel
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			stats.Run(runCtx) // 1 s batched flush of rule counters + decisions (W73 §5)
		}()
	}

	if a.Roles.Has(app.RoleAPI) {
		if srv, err := app.MustGet[*httpapi.Server](a); err == nil {
			m.registerAPI(srv)
		}
	}
	return nil
}

// Stop implements app.Module: stops the stats flusher after a final flush.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

// Engine returns the started engine (nil before Start).
func (m *Module) Engine() *Engine { return m.engine }

// Repo returns the started repo (nil before Start).
func (m *Module) Repo() *Repo { return m.repo }

// Migration creates routes, routeVersions and ruleErrors indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "rules indexes", Up: EnsureIndexes}
}

// LastErrorIndexMigration adds the ruleErrors index used for per-route lastError enrichment.
func LastErrorIndexMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "rules ruleErrors last-by-route", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, RuleErrorsCollection,
			mongo.IndexModel{Keys: bson.D{
				{Key: "organizationId", Value: 1}, {Key: "routeId", Value: 1}, {Key: "_id", Value: -1},
			}},
		)
	}}
}

// P31Migration adds organisation-scoped version list index (P3.1).
func P31Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "rules P3.1 versions org index", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, RouteVersionsCollection,
			mongo.IndexModel{Keys: bson.D{
				{Key: "organizationId", Value: 1}, {Key: "routeId", Value: 1}, {Key: "version", Value: -1},
			}},
		)
	}}
}

// W73Migration adds named lists, rule stats/decisions and endpoint rule version indexes.
func W73Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "rules W73 lists stats endpoint rules", Up: func(ctx context.Context, db *store.DB) error {
		if err := EnsureListIndexes(ctx, db); err != nil {
			return err
		}
		if err := EnsureStatsIndexes(ctx, db); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, "endpointRuleVersions",
			mongo.IndexModel{
				Keys:    bson.D{{Key: "endpointId", Value: 1}, {Key: "version", Value: -1}},
				Options: options.Index().SetUnique(true),
			},
			mongo.IndexModel{Keys: bson.D{
				{Key: "organizationId", Value: 1}, {Key: "endpointId", Value: 1}, {Key: "version", Value: -1},
			}},
		)
	}}
}
