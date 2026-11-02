package archive

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module provides the Archiver on every node, registers retention on the scheduler, and serves the
// raw/archive API on api nodes.
type Module struct {
	Accounts AccountLoader     // required for on-demand IMAP (accounts.SyncSource); set from cmd
	Idle     time.Duration     // on-demand idle close; default 60s
	IMAPID   map[string]string // global EB_IMAP_ID_* map, overlaid by the account's own (W57)
	Log      *slog.Logger

	store  *Store
	svc    *Service
	pool   *Pool
	cancel context.CancelFunc
	done   chan struct{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "archive" }

// Roles implements app.Module (platform: Archiver everywhere; API + pool on api).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	objs, err := app.MustGet[*objstore.Store](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.store = &Store{Objects: objs}
	app.Provide(a, m.store)

	dial, _ := app.Get[*egress.Dialer](a)
	pool, _ := app.Get[*ippool.Selector](a)
	m.pool = &Pool{Dialer: dial, IPPool: pool, IMAPID: m.IMAPID, Idle: m.Idle}
	m.svc = &Service{
		DB: db, Store: m.store, Pool: m.pool, Accounts: m.Accounts, Log: log,
	}

	if sched, ok := app.Get[*scheduler.Scheduler](a); ok {
		sched.Add("archive.retention", m.svc.RetentionDuty)
	}

	if a.Roles.Has(app.RoleAPI) {
		if m.Accounts == nil {
			return errors.New("archive: Accounts loader is required on api nodes")
		}
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.svc.Register(srv)
		pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.cancel = cancel
		m.done = make(chan struct{})
		go func() { defer close(m.done); m.pool.Run(pctx) }()
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
	return nil
}

// Archiver returns the started Store (nil before Start). Wire to sync.Module.Archive.
func (m *Module) Archiver() *Store { return m.store }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// TombstoneMigration indexes the archive tombstones of messages gone from the mailbox (retention per account).
func TombstoneMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "archive tombstones", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, index.ArchivedMessages,
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "internalDate", Value: 1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}}},
		)
	}}
}

// Migration adds a retention helper index on messages.archiveRef.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "archive retention index", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, index.Messages,
			mongo.IndexModel{
				Keys: bson.D{
					{Key: "accountId", Value: 1},
					{Key: "archiveRef", Value: 1},
					{Key: "internalDate", Value: 1},
				},
				Options: options.Index().SetName("archive_retention").
					SetPartialFilterExpression(bson.M{"archiveRef": bson.M{"$exists": true, "$gt": ""}}),
			},
		)
	}}
}
