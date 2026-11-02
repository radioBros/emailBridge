package cluster

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/queues"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module serves /v1/cluster, /v1/stats and /v1/queues on api nodes (P2.5).
type Module struct {
	Log *slog.Logger
	// Retention returns EB_RETENTION_* for the KPI trends (W74); nil uses DefaultRetention.
	Retention func() Retention
	// APIFlushInterval is how often API-call counters are flushed to MongoDB (default 1 min).
	APIFlushInterval time.Duration

	svc      *Service
	items    ItemCounter
	counter  *APICounter
	stopLoop context.CancelFunc
	loopDone chan struct{}
}

// ItemCounter returns the W72 record counts of one queue for the caller (nil when it may not read them).
// *queues.Service implements it; the queues module must start before this one.
type ItemCounter interface {
	Counts(ctx context.Context, queue string, all bool) (*queues.Counts, error)
}

// Name implements app.Module.
func (m *Module) Name() string { return "cluster" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.svc = &Service{DB: db, Bus: b, Log: log, NodeID: a.NodeID, Retention: m.Retention}
	if w, ok := app.Get[*audit.Writer](a); ok {
		m.svc.Audit = w
	}
	if q, ok := app.Get[*queues.Service](a); ok && q != nil {
		m.items = q
	}
	if err := m.svc.loadQueueSettings(ctx); err != nil {
		log.Warn("queue settings load failed; using defaults", "err", err)
	}
	m.registerAPI(srv)
	m.startCounter(srv, db, log)
	return nil
}

// startCounter adds the API-call counter middleware (inside tenancy, so it sees the organisation) and the
// per-minute flush loop (W74 §2).
func (m *Module) startCounter(srv *httpapi.Server, db *store.DB, log *slog.Logger) {
	m.counter = &APICounter{DB: db, Log: log}
	srv.AddMiddleware(m.counter.Middleware)
	interval := m.APIFlushInterval
	if interval <= 0 {
		interval = time.Minute
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	m.stopLoop = cancel
	m.loopDone = make(chan struct{})
	go func() {
		defer close(m.loopDone)
		m.counter.Run(loopCtx, interval)
	}()
}

// Stop implements app.Module: it stops the flush loop after a final flush.
func (m *Module) Stop(ctx context.Context) error {
	if m.stopLoop == nil {
		return nil
	}
	m.stopLoop()
	select {
	case <-m.loopDone:
	case <-ctx.Done():
	}
	m.stopLoop = nil
	return nil
}

// Counter returns the API-call counter (nil before Start).
func (m *Module) Counter() *APICounter { return m.counter }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration adds stats indexes used by GET /v1/stats (org + time / state aggregations).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "cluster stats indexes", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, "deliveries",
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "createdAt", Value: -1}},
				Options: options.Index().SetName("org_createdAt"),
			},
			mongo.IndexModel{
				Keys: bson.D{
					{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "createdAt", Value: -1},
				},
				Options: options.Index().SetName("org_state_createdAt"),
			},
		); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, "events",
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "time", Value: -1}},
				Options: options.Index().SetName("org_time"),
			},
		)
	}}
}

// KPIMigration adds the W74 dashboard indexes (migration 47): events by type + time, outbound by state +
// sentAt / createdAt, and the apiCallStats lookup + 90-day TTL.
func KPIMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "dashboard kpi indexes", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, "events", mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "type", Value: 1}, {Key: "time", Value: 1}},
			Options: options.Index().SetName("org_type_time"),
		}); err != nil {
			return err
		}
		if err := db.EnsureIndexes(ctx, "outboundMessages",
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "sentAt", Value: 1}},
				Options: options.Index().SetName("org_state_sentAt"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "createdAt", Value: 1}},
				Options: options.Index().SetName("org_state_createdAt"),
			},
		); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, APICallsCollection,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "hour", Value: 1}},
				Options: options.Index().SetName("org_hour"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "expireAt", Value: 1}},
				Options: options.Index().SetName("ttl_expireAt").SetExpireAfterSeconds(0),
			},
		)
	}}
}
