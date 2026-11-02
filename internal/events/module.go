package events

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	eventschemas "github.com/radiobros/emailbridge/api/events"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// RelayPrefix is the lease prefix of relay partitions (S4 §5: relay.<p>).
const RelayPrefix = "relay"

// Module provides *Writer on every node, runs the relay on sync/submit/api nodes and serves the schemas
// and events API on api nodes.
type Module struct {
	Partitions int                  // EB_EVENT_PARTITIONS
	Retention  func() time.Duration // EB_RETENTION_EVENTS
	LeaseTTL   time.Duration        // EB_LEASE_TTL
	Log        *slog.Logger
	// SSEEnabled overrides the events.sse switch (tests). nil → settings / default on.
	SSEEnabled func(ctx context.Context, orgID string) bool
	// KeepAlive overrides the SSE keepalive (tests).
	KeepAlive time.Duration

	relay  *Relay
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "events" }

// Roles implements app.Module (every node writes events).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	w := &Writer{DB: db, Partitions: m.Partitions, Retention: m.Retention}
	app.Provide(a, w)
	busInst, hasBus := app.Get[*bus.Bus](a)
	if srv, ok := app.Get[*httpapi.Server](a); ok && a.Roles.Has(app.RoleAPI) {
		srv.Router.Get("/schemas/events/envelope.json", envelopeSchemaHandler)
		srv.Router.Get("/schemas/events/{type}/v{version}.json", typeSchemaHandler)
		opts := APIOptions{SSEEnabled: m.SSEEnabled, KeepAlive: m.KeepAlive}
		if set, ok := app.Get[*settings.Service](a); ok {
			opts.Settings = set
		}
		if hasBus {
			opts.Subscriber = busInst
		}
		Register(srv, w, opts)
	}
	// The relay runs where events are written; its partitions are placed on exactly those nodes.
	relayRoles := []app.Role{app.RoleSync, app.RoleSubmit, app.RoleAPI}
	if !a.Roles.Any(relayRoles) {
		return nil
	}
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	m.relay = &Relay{DB: db, Pub: b, Partitions: m.Partitions, Log: log}
	if mtr, ok := app.Get[*observability.Metrics](a); ok {
		relay := m.relay
		mtr.ObserveRelayLag(relay.Lag)
	}
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	roles := make([]string, len(relayRoles))
	for i, r := range relayRoles {
		roles[i] = string(r)
	}
	bal := &bus.Balancer{Leases: b, Prefix: RelayPrefix, Roles: roles, N: m.relay.partitions(), NodeID: a.NodeID, TTL: m.LeaseTTL, Log: log, Work: m.relay.Work}
	m.wg.Add(2)
	go func() { defer m.wg.Done(); m.relay.Watch(rctx) }()
	go func() { defer m.wg.Done(); bal.Run(rctx) }()
	return nil
}

// Stop implements app.Module: stops the relay and releases its leases.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	return nil
}

// Relay returns the running relay (nil on nodes without a relay role).
func (m *Module) Relay() *Relay { return m.relay }

func (r *Relay) partitions() int {
	if r.Partitions > 0 {
		return r.Partitions
	}
	return 64
}

var (
	schemaName    = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)
	schemaVersion = regexp.MustCompile(`^[1-9][0-9]{0,2}$`)
)

// typeSchemaHandler serves /schemas/events/{type}/v{version}.json from the embedded files.
func typeSchemaHandler(w http.ResponseWriter, r *http.Request) {
	typ, ver := chi.URLParam(r, "type"), chi.URLParam(r, "version")
	if !schemaName.MatchString(typ) || !schemaVersion.MatchString(ver) {
		http.NotFound(w, r)
		return
	}
	serveSchema(w, r, typ+".v"+ver+".json")
}

func envelopeSchemaHandler(w http.ResponseWriter, r *http.Request) {
	serveSchema(w, r, "envelope.json")
}

// serveSchema writes an embedded schema (public, cacheable).
func serveSchema(w http.ResponseWriter, r *http.Request, file string) {
	b, err := fs.ReadFile(eventschemas.FS, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b) //nolint:gosec // G705: embedded static schema files served as application/schema+json, no user input in the body
}
