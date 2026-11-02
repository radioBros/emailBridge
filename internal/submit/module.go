package submit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// durable is the JetStream consumer of the submit workers (S4 §4).
const durable = "submit"

// Module wires the submit API, the SMTP workers and the scheduler duties of the send pipeline (S6).
type Module struct {
	// Accounts resolves SMTP settings; cmd provides the adapter over internal/accounts.
	Accounts AccountSMTP
	// Sent appends sent mail to the Sent folder; nil records copyToSent as skipped (S6 §5).
	Sent SentCopier
	// Sandbox is the P1.14 gate; nil sends everything.
	Sandbox SandboxGate

	Concurrency    int
	MaxMessageSize int64
	Policy         string
	MaxAttempts    int
	BackoffBase    time.Duration
	BackoffJitter  float64
	PoolMaxMsgs    int
	PoolIdle       time.Duration
	PoolPerAccount int
	IdempotencyTTL time.Duration
	// QuotaSendsPerDay and QuotaSendsPerMinute are EB_QUOTA_SENDS_* (S9 §2.12).
	QuotaSendsPerDay    int
	QuotaSendsPerMinute int
	// EHLO is the fallback EHLO name (host of EB_PUBLIC_URL).
	EHLO string
	Log  *slog.Logger

	service *Service
	stop    func()
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "submit" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleSubmit} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	objects, err := app.MustGet[*objstore.Store](a)
	if err != nil {
		return err
	}
	eventStore, err := app.MustGet[*events.Writer](a)
	if err != nil {
		return err
	}
	eventBus, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	dialer, err := app.MustGet[*egress.Dialer](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	repo := &Repo{DB: db}
	aud, _ := app.Get[*audit.Writer](a)
	pool, _ := app.Get[*ippool.Selector](a)
	m.service = &Service{
		Repo: repo, Accounts: m.Accounts, Objects: objects, Events: eventStore, Publisher: eventBus,
		Sent: m.Sent, Sandbox: m.Sandbox, Log: log, NodeID: a.NodeID, EHLO: m.EHLO, Audit: aud, IPPool: pool,
		MaxMessageSize: m.MaxMessageSize, Policy: m.Policy, MaxAttempts: m.MaxAttempts,
		BackoffBase: m.BackoffBase, BackoffJitter: m.BackoffJitter, IdempotencyTTL: m.IdempotencyTTL,
		QuotaSendsPerDay: m.QuotaSendsPerDay, QuotaSendsPerMinute: m.QuotaSendsPerMinute,
	}
	m.service.Pool = &Pool{
		Dial: m.service.dial(dialer), MaxMessages: m.PoolMaxMsgs, Idle: m.PoolIdle, PerAccount: m.PoolPerAccount,
	}
	app.Provide(a, repo)
	app.Provide(a, m.service)

	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
	}
	// Both duties run only on the scheduler leader; every node registers them (S6 §2.7, §3.7).
	if sched, ok := app.Get[*scheduler.Scheduler](a); ok && sched != nil {
		sched.Add("submit-due", m.service.Due)
		sched.Add("submit-sweep", m.service.Sweep)
	}
	if !a.Roles.Has(app.RoleSubmit) {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.service.Pool.Run(runCtx)
	}()
	concurrency := m.Concurrency
	if concurrency <= 0 {
		concurrency = 32
	}
	stop, err := eventBus.ConsumePartition(runCtx, bus.StreamSubmit, durable, bus.SubmitSubject("*"),
		concurrency, m.service.Work)
	if err != nil {
		cancel()
		m.wg.Wait()
		return err
	}
	m.stop = stop
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.stop != nil {
		m.stop()
	}
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	return nil
}

// Service returns the started service (cmd wires the accounts adapter and the sync copier into it).
func (m *Module) Service() *Service { return m.service }

var _ Publisher = (*bus.Bus)(nil)
var _ RawStore = (*objstore.Store)(nil)
