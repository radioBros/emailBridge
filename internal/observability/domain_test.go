package observability_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/observability"
)

func TestDomainCounters(t *testing.T) {
	m := observability.NewMetrics()
	m.IncError("temporary", "sync", "imap")
	m.IncAuthFailure("password")
	m.IncDeadLetter(observability.QueueDelivery)
	m.IncLeaseChange(observability.LeaseClaimed)
	m.LeaseChange(observability.LeaseLost)
	m.AuthFailure("mfa")
	m.DeadLetter(observability.QueueSubmit)

	assertCounter(t, m, "eb_errors_total", 1, "kind", "temporary", "component", "sync", "provider", "imap")
	assertCounter(t, m, "eb_auth_failures_total", 1, "provider", "password")
	assertCounter(t, m, "eb_auth_failures_total", 1, "provider", "mfa")
	assertCounter(t, m, "eb_deadletters_total", 1, "queue", "delivery")
	assertCounter(t, m, "eb_deadletters_total", 1, "queue", "submit")
	assertCounter(t, m, "eb_lease_changes_total", 1, "kind", "claimed")
	assertCounter(t, m, "eb_lease_changes_total", 1, "kind", "lost")
}

func TestDomainGaugesAndHTTP(t *testing.T) {
	m := observability.NewMetrics()
	m.SetRelayLagSeconds(45)
	m.SetSyncStats("n1", 4, 1, 100)
	m.SetStreamStats("EMAILBRIDGE_SUBMIT", 7, 2, 1)
	m.SetMigrationsPending(2)

	assertGauge(t, m, "eb_relay_lag_seconds", 45)
	assertGauge(t, m, "eb_sync_accounts", 4, "node", "n1")
	assertGauge(t, m, "eb_sync_unplaced_accounts", 1, "node", "n1")
	assertGauge(t, m, "eb_sync_max_accounts", 100)
	assertGauge(t, m, "eb_stream_pending", 7, "stream", "EMAILBRIDGE_SUBMIT")
	assertGauge(t, m, "eb_stream_ack_pending", 2, "stream", "EMAILBRIDGE_SUBMIT")
	assertGauge(t, m, "eb_stream_redeliveries", 1, "stream", "EMAILBRIDGE_SUBMIT")
	assertGauge(t, m, "eb_migrations_pending", 2)
	assertGauge(t, m, "eb_account_state", 0, "state", "connected")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Serve(ctx, addr) }()
	defer func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("serve: %v", err)
		}
	}()

	body := waitMetrics(t, addr)
	for _, want := range []string{
		"eb_account_state", "eb_endpoint_breaker_open", "eb_log_disk_bytes",
		"eb_errors_total", "eb_lease_changes_total", "eb_auth_failures_total",
		"eb_relay_lag_seconds", "eb_sync_accounts", "eb_stream_pending",
		"eb_insecure_tls_accounts", "eb_rule_errors_recent", "eb_migrations_pending",
	} {
		if !strings.Contains(body, "# TYPE "+want+" ") && !strings.Contains(body, "# TYPE "+want+"\n") && !strings.Contains(body, want+"{") && !strings.Contains(body, want+" ") {
			t.Fatalf("missing %s in:\n%s", want, body)
		}
	}
}

func TestCollectorRefreshViaModule(t *testing.T) {
	mod := &observability.Module{Options: observability.ModuleOptions{}}
	a := app.New(app.Options{NodeID: "n1", Modules: []app.Module{mod}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.Stop(ctx)
	}()
	m, err := app.MustGet[*observability.Metrics](a)
	if err != nil {
		t.Fatal(err)
	}
	m.ObserveMongo(func(context.Context) (observability.MongoCounts, error) {
		return observability.MongoCounts{
			AccountState:     map[string]int64{"connected": 3},
			BreakerOpen:      map[string]int64{"org_x": 1},
			InsecureTLS:      2,
			RuleErrorsRecent: 6,
		}, nil
	})
	m.ObserveLogDisk(fakeDisk{usage: map[string]int64{"api": 42}, cap: 1000})
	m.ObserveSync("node", 10, func() (int64, int64) { return 2, 0 })
	m.ObserveRelayLag(func(context.Context) (time.Duration, error) { return 2 * time.Second, nil })
	m.ObserveStreams(func(context.Context) ([]observability.StreamStat, error) {
		return []observability.StreamStat{{
			Stream: "EMAILBRIDGE_EVENTS", Pending: 9, AckPending: 3, Redeliveries: 1,
		}}, nil
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gaugeValue(t, m, "eb_account_state", "state", "connected") == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assertGauge(t, m, "eb_sync_accounts", 2, "node", "node")
	assertGauge(t, m, "eb_account_state", 3, "state", "connected")
	assertGauge(t, m, "eb_endpoint_breaker_open", 1, "organization", "org_x")
	assertGauge(t, m, "eb_insecure_tls_accounts", 2)
	assertGauge(t, m, "eb_rule_errors_recent", 6)
	assertGauge(t, m, "eb_log_disk_bytes", 42, "category", "api")
	assertGauge(t, m, "eb_log_disk_cap_bytes", 1000)
	assertGauge(t, m, "eb_relay_lag_seconds", 2)
	assertGauge(t, m, "eb_stream_pending", 9, "stream", "EMAILBRIDGE_EVENTS")
	assertGauge(t, m, "eb_stream_ack_pending", 3, "stream", "EMAILBRIDGE_EVENTS")
}

func TestNilMetricsNoPanic(t *testing.T) {
	var m *observability.Metrics
	m.IncError("x", "y", "z")
	m.IncAuthFailure("p")
	m.IncDeadLetter("q")
	m.IncLeaseChange("claimed")
	m.SetRelayLagSeconds(1)
	m.SetSyncStats("n", 1, 0, 1)
	m.SetStreamStats("s", 1, 1, 1)
	m.SetMigrationsPending(0)
	m.ObserveSync("n", 1, nil)
	m.ObserveRelayLag(nil)
	m.ObserveStreams(nil)
	m.ObserveLogDisk(nil)
	m.ObserveMongo(nil)
}

func TestEmptyLabelBecomesNone(t *testing.T) {
	m := observability.NewMetrics()
	m.IncError("", "", "")
	assertCounter(t, m, "eb_errors_total", 1, "kind", "none", "component", "none", "provider", "none")
}

type fakeDisk struct {
	usage map[string]int64
	cap   int64
}

func (f fakeDisk) MetricDiskUsage() (map[string]int64, error) { return f.usage, nil }
func (f fakeDisk) DiskCap() int64                             { return f.cap }

func gather(t *testing.T, m *observability.Metrics) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

func assertCounter(t *testing.T, m *observability.Metrics, name string, want float64, labels ...string) {
	t.Helper()
	got := findMetric(t, m, name, labels...)
	if got.GetCounter() == nil || got.GetCounter().GetValue() != want {
		t.Fatalf("%s%v: got %v want %v", name, labels, got.GetCounter(), want)
	}
}

func assertGauge(t *testing.T, m *observability.Metrics, name string, want float64, labels ...string) {
	t.Helper()
	got := findMetric(t, m, name, labels...)
	if got.GetGauge() == nil || got.GetGauge().GetValue() != want {
		t.Fatalf("%s%v: got %v want %v", name, labels, got.GetGauge(), want)
	}
}

func gaugeValue(t *testing.T, m *observability.Metrics, name string, labels ...string) float64 {
	t.Helper()
	mf := gather(t, m)[name]
	if mf == nil {
		return -1
	}
	want := labelMap(labels...)
	for _, metric := range mf.GetMetric() {
		if labelsMatch(metric, want) {
			if metric.GetGauge() == nil {
				return -1
			}
			return metric.GetGauge().GetValue()
		}
	}
	return -1
}

func findMetric(t *testing.T, m *observability.Metrics, name string, labels ...string) *dto.Metric {
	t.Helper()
	mf := gather(t, m)[name]
	if mf == nil {
		t.Fatalf("missing family %s", name)
	}
	want := labelMap(labels...)
	for _, metric := range mf.GetMetric() {
		if labelsMatch(metric, want) {
			return metric
		}
	}
	t.Fatalf("no series %s%v", name, labels)
	return nil
}

func labelMap(labels ...string) map[string]string {
	want := map[string]string{}
	for i := 0; i+1 < len(labels); i += 2 {
		want[labels[i]] = labels[i+1]
	}
	return want
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
	if len(want) == 0 {
		return len(metric.GetLabel()) == 0
	}
	have := map[string]string{}
	for _, l := range metric.GetLabel() {
		have[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func waitMetrics(t *testing.T, addr string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return string(b)
	}
	t.Fatal("metrics endpoint not up")
	return ""
}
