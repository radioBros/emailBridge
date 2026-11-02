package observability_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/observability"
)

func TestMongoCollectorNil(t *testing.T) {
	if observability.MongoCollector(nil) != nil {
		t.Fatal("expected nil func")
	}
}

func TestRefreshIgnoresSourceErrors(t *testing.T) {
	m := observability.NewMetrics()
	m.SetRelayLagSeconds(9)
	m.ObserveRelayLag(func(context.Context) (time.Duration, error) {
		return 0, errors.New("lag down")
	})
	m.ObserveStreams(func(context.Context) ([]observability.StreamStat, error) {
		return nil, errors.New("streams down")
	})
	m.ObserveLogDisk(errDisk{})
	m.ObserveMongo(func(context.Context) (observability.MongoCounts, error) {
		return observability.MongoCounts{}, errors.New("mongo down")
	})
	// Lag should stay at last good value from SetRelayLagSeconds before ObserveRelayLag's refresh failed.
	assertGauge(t, m, "eb_relay_lag_seconds", 9)
}

func TestApplyMongoEmptyOrgAndMissingStates(t *testing.T) {
	m := observability.NewMetrics()
	m.ObserveMongo(func(context.Context) (observability.MongoCounts, error) {
		return observability.MongoCounts{
			AccountState: map[string]int64{"connected": 1},
			BreakerOpen:  map[string]int64{"": 3},
		}, nil
	})
	assertGauge(t, m, "eb_account_state", 1, "state", "connected")
	assertGauge(t, m, "eb_account_state", 0, "state", "new")
	assertGauge(t, m, "eb_endpoint_breaker_open", 3, "organization", "none")
}

func TestMetricsShutdownWhileServing(t *testing.T) {
	m := observability.NewMetrics()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Serve(ctx, "127.0.0.1:0") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not stop")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type errDisk struct{}

func (errDisk) MetricDiskUsage() (map[string]int64, error) { return nil, errors.New("disk") }
func (errDisk) DiskCap() int64                             { return 0 }
