package observability

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Lease change kinds for eb_lease_changes_total (S12 §9, S4 §9).
const (
	LeaseClaimed   = "claimed"
	LeaseLost      = "lost"
	LeaseReleased  = "released"
	LeaseHandedOff = "handed_off"
)

// Queue names for eb_deadletters_total.
const (
	QueueDelivery = "delivery"
	QueueSubmit   = "submit"
	QueueSysmail  = "sysmail"
)

// Stream names for per-stream gauges (S4 §1 / §9).
var knownStreams = []string{
	"EMAILBRIDGE_EVENTS",
	"EMAILBRIDGE_DELIVERY",
	"EMAILBRIDGE_SUBMIT",
	"EMAILBRIDGE_SYSMAIL",
}

// AccountStates lists low-cardinality state labels for eb_account_state.
var AccountStates = []string{
	"new", "connecting", "syncing", "connected",
	"authError", "connectError", "disconnected", "disabled", "deleting",
}

// domainMetrics holds the S12 §9 domain counters and gauges.
type domainMetrics struct {
	errorsTotal      *prometheus.CounterVec
	authFailures     *prometheus.CounterVec
	deadletters      *prometheus.CounterVec
	leaseChanges     *prometheus.CounterVec
	accountState     *prometheus.GaugeVec
	breakerOpen      *prometheus.GaugeVec
	relayLag         prometheus.Gauge
	syncAccounts     *prometheus.GaugeVec
	syncUnplaced     *prometheus.GaugeVec
	syncMaxAccounts  prometheus.Gauge
	streamPending    *prometheus.GaugeVec
	streamAckPend    *prometheus.GaugeVec
	streamRedeliv    *prometheus.GaugeVec
	logDiskBytes     *prometheus.GaugeVec
	logDiskCap       prometheus.Gauge
	migrationsPend   prometheus.Gauge
	insecureTLS      prometheus.Gauge
	ruleErrorsRecent prometheus.Gauge
}

func newDomainMetrics() *domainMetrics {
	return &domainMetrics{
		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_errors_total",
			Help: "Errors by kind, component and mail provider",
		}, []string{"kind", "component", "provider"}),
		authFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_auth_failures_total",
			Help: "Authentication failures by provider",
		}, []string{"provider"}),
		deadletters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_deadletters_total",
			Help: "Messages moved to a dead-letter queue",
		}, []string{"queue"}),
		leaseChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_lease_changes_total",
			Help: "Lease lifecycle events (claimed, lost, released, handed_off)",
		}, []string{"kind"}),
		accountState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_account_state",
			Help: "Accounts by connection state (MongoDB counts, refreshed periodically)",
		}, []string{"state"}),
		breakerOpen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_endpoint_breaker_open",
			Help: "Webhook endpoints whose circuit breaker is open, by organisation",
		}, []string{"organization"}),
		relayLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_relay_lag_seconds",
			Help: "Age of the oldest unpublished event (events.Relay lag)",
		}),
		syncAccounts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_sync_accounts",
			Help: "Accounts currently synced on a node",
		}, []string{"node"}),
		syncUnplaced: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_sync_unplaced_accounts",
			Help: "Accounts that could not be placed on a node (over EB_SYNC_MAX_ACCOUNTS)",
		}, []string{"node"}),
		syncMaxAccounts: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_sync_max_accounts",
			Help: "EB_SYNC_MAX_ACCOUNTS capacity per sync node (0 = unlimited)",
		}),
		streamPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_stream_pending",
			Help: "JetStream messages pending per stream",
		}, []string{"stream"}),
		streamAckPend: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_stream_ack_pending",
			Help: "JetStream ack-pending messages per stream",
		}, []string{"stream"}),
		streamRedeliv: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_stream_redeliveries",
			Help: "JetStream redeliveries per stream",
		}, []string{"stream"}),
		logDiskBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_log_disk_bytes",
			Help: "Log store disk usage per category",
		}, []string{"category"}),
		logDiskCap: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_log_disk_cap_bytes",
			Help: "Configured log disk cap (EB / settings diskCap)",
		}),
		migrationsPend: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_migrations_pending",
			Help: "MongoDB migrations known to this binary but not yet applied",
		}),
		insecureTLS: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_insecure_tls_accounts",
			Help: "Accounts with accounts.ignoreCertErrors enabled",
		}),
		ruleErrorsRecent: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_rule_errors_recent",
			Help: "ruleErrors documents written in the last 15 minutes",
		}),
	}
}

func (d *domainMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		d.errorsTotal, d.authFailures, d.deadletters, d.leaseChanges,
		d.accountState, d.breakerOpen, d.relayLag,
		d.syncAccounts, d.syncUnplaced, d.syncMaxAccounts,
		d.streamPending, d.streamAckPend, d.streamRedeliv,
		d.logDiskBytes, d.logDiskCap,
		d.migrationsPend, d.insecureTLS, d.ruleErrorsRecent,
	}
}

func (d *domainMetrics) zeroGauges() {
	for _, st := range AccountStates {
		d.accountState.WithLabelValues(st).Set(0)
	}
	for _, s := range knownStreams {
		d.streamPending.WithLabelValues(s).Set(0)
		d.streamAckPend.WithLabelValues(s).Set(0)
		d.streamRedeliv.WithLabelValues(s).Set(0)
	}
	// Seed series so /metrics always exposes the names (Prometheus omits empty vecs).
	d.breakerOpen.WithLabelValues("none").Set(0)
	d.syncAccounts.WithLabelValues("none").Set(0)
	d.syncUnplaced.WithLabelValues("none").Set(0)
	d.logDiskBytes.WithLabelValues("none").Set(0)
	d.relayLag.Set(0)
	d.syncMaxAccounts.Set(0)
	d.logDiskCap.Set(0)
	d.migrationsPend.Set(0)
	d.insecureTLS.Set(0)
	d.ruleErrorsRecent.Set(0)
	// CounterVec series appear only after an observation; seed zeros.
	d.errorsTotal.WithLabelValues("none", "none", "none").Add(0)
	d.authFailures.WithLabelValues("none").Add(0)
	d.deadletters.WithLabelValues("none").Add(0)
	d.leaseChanges.WithLabelValues(LeaseClaimed).Add(0)
	d.leaseChanges.WithLabelValues(LeaseLost).Add(0)
	d.leaseChanges.WithLabelValues(LeaseReleased).Add(0)
	d.leaseChanges.WithLabelValues(LeaseHandedOff).Add(0)
}

// IncError increments eb_errors_total.
func (m *Metrics) IncError(kind, component, provider string) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.errorsTotal.WithLabelValues(emptyAsNone(kind), emptyAsNone(component), emptyAsNone(provider)).Inc()
}

// IncAuthFailure increments eb_auth_failures_total.
func (m *Metrics) IncAuthFailure(provider string) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.authFailures.WithLabelValues(emptyAsNone(provider)).Inc()
}

// IncDeadLetter increments eb_deadletters_total.
func (m *Metrics) IncDeadLetter(queue string) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.deadletters.WithLabelValues(emptyAsNone(queue)).Inc()
}

// IncLeaseChange increments eb_lease_changes_total.
func (m *Metrics) IncLeaseChange(kind string) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.leaseChanges.WithLabelValues(emptyAsNone(kind)).Inc()
}

// LeaseChange implements sync's LeaseObserver.
func (m *Metrics) LeaseChange(kind string) { m.IncLeaseChange(kind) }

// AuthFailure implements auth's failure recorder.
func (m *Metrics) AuthFailure(provider string) { m.IncAuthFailure(provider) }

// DeadLetter implements webhook's dead-letter recorder.
func (m *Metrics) DeadLetter(queue string) { m.IncDeadLetter(queue) }

// SetRelayLagSeconds sets eb_relay_lag_seconds.
func (m *Metrics) SetRelayLagSeconds(seconds float64) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.relayLag.Set(seconds)
}

// SetSyncStats sets eb_sync_accounts / eb_sync_unplaced_accounts / eb_sync_max_accounts.
func (m *Metrics) SetSyncStats(node string, accounts, unplaced int64, maxAccounts int) {
	if m == nil || m.domain == nil {
		return
	}
	n := emptyAsNone(node)
	m.domain.syncAccounts.WithLabelValues(n).Set(float64(accounts))
	m.domain.syncUnplaced.WithLabelValues(n).Set(float64(unplaced))
	m.domain.syncMaxAccounts.Set(float64(maxAccounts))
}

// SetStreamStats sets per-stream pending / ack_pending / redeliveries.
func (m *Metrics) SetStreamStats(stream string, pending, ackPending, redeliveries float64) {
	if m == nil || m.domain == nil {
		return
	}
	s := emptyAsNone(stream)
	m.domain.streamPending.WithLabelValues(s).Set(pending)
	m.domain.streamAckPend.WithLabelValues(s).Set(ackPending)
	m.domain.streamRedeliv.WithLabelValues(s).Set(redeliveries)
}

// SetMigrationsPending sets eb_migrations_pending.
func (m *Metrics) SetMigrationsPending(n int) {
	if m == nil || m.domain == nil {
		return
	}
	m.domain.migrationsPend.Set(float64(n))
}

func emptyAsNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// SyncStatsFunc returns accounts and unplaced for a sync node.
type SyncStatsFunc func() (accounts, unplaced int64)

// RelayLagFunc returns the events relay lag.
type RelayLagFunc func(ctx context.Context) (time.Duration, error)

// StreamStat is one JetStream stream's consumer-aggregated counts.
type StreamStat struct {
	Stream       string
	Pending      float64
	AckPending   float64
	Redeliveries float64
}

// StreamStatsFunc returns pending / ack-pending / redeliveries per stream.
type StreamStatsFunc func(ctx context.Context) ([]StreamStat, error)

// LogDiskSource reports log store disk usage and the configured cap.
type LogDiskSource interface {
	MetricDiskUsage() (map[string]int64, error)
	DiskCap() int64
}

// MongoCounts are periodic aggregates from MongoDB (account state, breakers, …).
type MongoCounts struct {
	AccountState     map[string]int64
	BreakerOpen      map[string]int64 // organization → open breakers
	InsecureTLS      int64
	RuleErrorsRecent int64
}

// MongoCountsFunc refreshes Mongo-backed gauges.
type MongoCountsFunc func(ctx context.Context) (MongoCounts, error)

type sources struct {
	mu        sync.Mutex
	syncNode  string
	syncMax   int
	syncStats SyncStatsFunc
	relayLag  RelayLagFunc
	streams   StreamStatsFunc
	logDisk   LogDiskSource
	mongo     MongoCountsFunc
}

// ObserveSync registers the sync manager's Stats for eb_sync_* gauges.
func (m *Metrics) ObserveSync(node string, maxAccounts int, stats SyncStatsFunc) {
	if m == nil || stats == nil {
		return
	}
	m.sources.mu.Lock()
	m.sources.syncNode = node
	m.sources.syncMax = maxAccounts
	m.sources.syncStats = stats
	m.sources.mu.Unlock()
	accounts, unplaced := stats()
	m.SetSyncStats(node, accounts, unplaced, maxAccounts)
}

// ObserveRelayLag registers events.Relay.Lag for eb_relay_lag_seconds.
func (m *Metrics) ObserveRelayLag(fn RelayLagFunc) {
	if m == nil {
		return
	}
	m.sources.mu.Lock()
	m.sources.relayLag = fn
	m.sources.mu.Unlock()
	m.refresh(context.Background())
}

// ObserveStreams registers JetStream stream stats (wired from bus; see brief 28a summary).
func (m *Metrics) ObserveStreams(fn StreamStatsFunc) {
	if m == nil {
		return
	}
	m.sources.mu.Lock()
	m.sources.streams = fn
	m.sources.mu.Unlock()
	m.refresh(context.Background())
}

// ObserveLogDisk registers log store disk usage for eb_log_disk_bytes / eb_log_disk_cap_bytes.
func (m *Metrics) ObserveLogDisk(src LogDiskSource) {
	if m == nil || src == nil {
		return
	}
	m.sources.mu.Lock()
	m.sources.logDisk = src
	m.sources.mu.Unlock()
	m.refresh(context.Background())
}

// ObserveMongo registers MongoDB aggregates for account state, breakers, TLS and rule errors.
func (m *Metrics) ObserveMongo(fn MongoCountsFunc) {
	if m == nil {
		return
	}
	m.sources.mu.Lock()
	m.sources.mongo = fn
	m.sources.mu.Unlock()
	m.refresh(context.Background())
}
