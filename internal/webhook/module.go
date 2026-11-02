package webhook

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires webhook APIs and the partitioned router/delivery workers.
type Module struct {
	EventPartitions    int
	DeliveryPartitions int
	Concurrency        int
	LeaseTTL           time.Duration
	ResponseExcerpt    int
	Retention          time.Duration
	RotationOverlap    time.Duration
	DefaultMaxAttempts int
	DefaultBackoffBase time.Duration
	DefaultJitter      float64
	DefaultTimeout     time.Duration
	DefaultAutoDisable time.Duration
	SecretResolver     SecretResolver
	RulesEnabled       func(context.Context, string) bool
	NotifyDisabled     func(context.Context, *Endpoint)
	QuotaEndpoints     int
	Log                *slog.Logger
	SecurityLog        *slog.Logger

	service *Service
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "webhook" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI, app.RoleWebhook} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	cipher, err := app.MustGet[*crypto.Cipher](a)
	if err != nil {
		return err
	}
	dialer, err := app.MustGet[*egress.Dialer](a)
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
	ruleEngine, err := app.MustGet[*rules.Engine](a)
	if err != nil {
		return err
	}
	ruleRepo, err := app.MustGet[*rules.Repo](a)
	if err != nil {
		return err
	}
	auditor, _ := app.Get[*audit.Writer](a)
	log := m.Log
	if log == nil {
		log = a.Log
	}
	repo := &Repo{DB: db}
	js, _ := app.Get[*jobs.Service](a)
	set, _ := app.Get[*settings.Service](a)
	m.service = &Service{
		Repo: repo, Cipher: cipher, Egress: dialer, Events: eventStore,
		Rules: ruleRepo, Engine: ruleEngine, Publisher: eventBus, Audit: auditor,
		Jobs: js, Settings: set,
		Log: log, SecurityLog: m.SecurityLog, Version: a.Build.Version,
		Concurrency: m.Concurrency, DeliveryPartitions: m.DeliveryPartitions,
		ResponseExcerpt: m.ResponseExcerpt, Retention: m.Retention, RotationOverlap: m.RotationOverlap,
		DefaultMaxAttempts: m.DefaultMaxAttempts, DefaultBackoffBase: m.DefaultBackoffBase,
		DefaultJitter: m.DefaultJitter, DefaultTimeout: m.DefaultTimeout, DefaultAutoDisable: m.DefaultAutoDisable,
		SecretResolver: m.SecretResolver, RulesEnabled: m.RulesEnabled, NotifyDisabled: m.NotifyDisabled,
		QuotaEndpoints: m.QuotaEndpoints, scheduler: newScheduler(),
	}
	if mtr, ok := app.Get[*observability.Metrics](a); ok {
		m.service.Metrics = mtr
	}
	m.service.RegisterHandlers()
	app.Provide(a, repo)
	app.Provide(a, m.service)

	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
	}
	if !a.Roles.Has(app.RoleWebhook) {
		return nil
	}
	eventPartitions := m.EventPartitions
	if eventPartitions <= 0 {
		eventPartitions = 64
	}
	deliveryPartitions := m.DeliveryPartitions
	if deliveryPartitions <= 0 {
		deliveryPartitions = 256
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	router := &bus.Balancer{
		Leases: eventBus, Prefix: "evt", Roles: []string{string(app.RoleWebhook)}, N: eventPartitions, NodeID: a.NodeID,
		TTL: m.LeaseTTL, Log: log, Work: m.service.RouterWork(eventBus),
	}
	delivery := &bus.Balancer{
		Leases: eventBus, Prefix: "dlv", Roles: []string{string(app.RoleWebhook)}, N: deliveryPartitions, NodeID: a.NodeID,
		TTL: m.LeaseTTL, Log: log, Work: m.service.DeliveryWork(eventBus),
	}
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		router.Run(runCtx)
	}()
	go func() {
		defer m.wg.Done()
		delivery.Run(runCtx)
	}()
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	return nil
}

// Service returns the started service.
func (m *Module) Service() *Service { return m.service }
