package attachments

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the attachments service on api nodes.
type Module struct {
	Fetcher     Fetcher
	Raw         RawSource
	ClamAVAddr  string
	SignKey     []byte
	PublicURL   string
	SignedTTL   time.Duration
	ScanMaxSize int64
	ScanTimeout time.Duration
	Log         *slog.Logger

	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "attachments" }

// Roles implements app.Module (API routes on api; Ingest available everywhere via Provide).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	objs, _ := app.Get[*objstore.Store](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	ttl := m.SignedTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	maxSize := m.ScanMaxSize
	if maxSize <= 0 {
		maxSize = 25 << 20
	}
	scanTO := m.ScanTimeout
	if scanTO <= 0 {
		scanTO = 30 * time.Second
	}
	svc := &Service{
		DB: db, Objects: objs, Fetcher: m.Fetcher, Raw: m.Raw,
		Scanner: &Scanner{Addr: m.ClamAVAddr, Timeout: scanTO, MaxSize: maxSize},
		SignKey: m.SignKey, PublicURL: m.PublicURL, SignedTTL: ttl, Log: log,
	}
	svc.init()
	if err := svc.store.ensure(ctx); err != nil {
		return err
	}
	m.svc = svc
	app.Provide(a, svc)

	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		svc.Register(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration creates attachment indexes (number from the brief: 27).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "attachments index", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "messageId", Value: 1}, {Key: "partId", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("att_msg_part"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "messageId", Value: 1}},
				Options: options.Index().SetName("att_msg"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}},
				Options: options.Index().SetName("att_org"),
			},
		)
	}}
}
