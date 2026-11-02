package messages

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
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// AccountLoader loads decrypted IMAP settings (accounts.SyncSource).
type AccountLoader interface {
	Load(ctx context.Context, accountID string) (*ebsync.Account, error)
}

// Submitter sends a draft via the outbound pipeline (submit.Service).
type Submitter interface {
	Submit(ctx context.Context, accountID string, req submit.SubmitRequest, key string, includeRaw bool) (*submit.SubmitResult, error)
}

// Module serves the mailboxes/messages/drafts API on api nodes.
type Module struct {
	Accounts  AccountLoader
	Submit    Submitter
	Providers OpsFactory // gmail/graph mailbox ops (W50); nil → IMAP only
	Idle      time.Duration
	IMAPID    map[string]string // global EB_IMAP_ID_* map, overlaid by the account's own (W57)
	Log       *slog.Logger

	pool   *ondemand.Pool
	svc    *Service
	cancel context.CancelFunc
	done   chan struct{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "messages" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if m.Accounts == nil {
		return errors.New("messages: Accounts loader is required")
	}
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	dial, _ := app.Get[*egress.Dialer](a)
	ipp, _ := app.Get[*ippool.Selector](a)
	m.pool = &ondemand.Pool{Dialer: dial, IPPool: ipp, IMAPID: m.IMAPID, Idle: m.Idle, Metrics: ondemand.NewMetrics()}
	m.svc = &Service{
		DB: db, Pool: m.pool, Accounts: m.Accounts, Submit: m.Submit, Log: log,
		Attachments: &MongoAttachmentLister{DB: db},
		Providers:   m.Providers,
	}
	if sub, ok := app.Get[*submit.Service](a); ok && m.Submit == nil {
		m.svc.Submit = sub
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	m.svc.Register(srv)
	app.Provide(a, m.svc)
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() { defer close(m.done); m.pool.Run(pctx) }()
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

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration indexes the drafts collection (P4.1, migration 24).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "messages drafts indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Drafts,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "updatedAt", Value: -1}},
				Options: options.Index().SetName("drafts_account_updated"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}},
				Options: options.Index().SetName("drafts_org_account"),
			},
		)
	}}
}
