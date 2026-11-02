package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/observability"
)

func TestReporterApplyClearConnector(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://k@" + host + "/1"

	r, err := observability.NewReporter(observability.ReporterOptions{
		NodeID: "n", Role: "api", Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Enabled() || r.EnvLocked() {
		t.Fatal("expected disabled unlocked")
	}
	r.CaptureError(context.Background(), errors.New("skip"), nil)
	if hits.Load() != 0 {
		t.Fatal("disabled should not send")
	}

	if err := r.ApplyConnector(observability.ReporterOptions{DSN: dsn, Environment: "test", SampleRate: 1}); err != nil {
		t.Fatal(err)
	}
	if !r.Enabled() {
		t.Fatal("expected enabled")
	}
	r.CaptureError(context.Background(), errors.New("boom"), map[string]string{"code": "c"})
	r.Flush(2 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("expected event after ApplyConnector")
	}

	r.ClearConnector()
	if r.Enabled() {
		t.Fatal("cleared")
	}
	before := hits.Load()
	r.CaptureError(context.Background(), errors.New("again"), nil)
	r.Flush(time.Second)
	if hits.Load() != before {
		t.Fatal("cleared should not send")
	}
}

func TestReporterEnvDSNPrecedence(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	envDSN := "http://env@" + host + "/1"

	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN: envDSN, SampleRate: 1, Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.EnvLocked() || !r.Enabled() {
		t.Fatal("env should lock")
	}
	_ = r.ApplyConnector(observability.ReporterOptions{DSN: "http://other@example/9", SampleRate: 1})
	r.ClearConnector()
	if !r.Enabled() {
		t.Fatal("env DSN must survive ClearConnector")
	}
	r.CaptureError(context.Background(), errors.New("env"), nil)
	r.Flush(2 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("env reporter should still send")
	}
}

func TestReporterApplyConnectorEdges(t *testing.T) {
	var r *observability.Reporter
	if err := r.ApplyConnector(observability.ReporterOptions{DSN: "x"}); err != nil {
		t.Fatal(err)
	}
	r.ClearConnector()

	r, err := observability.NewReporter(observability.ReporterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyConnector(observability.ReporterOptions{}); err != nil {
		t.Fatal(err)
	}
	if r.Enabled() {
		t.Fatal("empty apply should clear")
	}
	if err := r.ApplyConnector(observability.ReporterOptions{DSN: "://bad"}); err == nil {
		t.Fatal("expected bad dsn error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	err = r.ApplyConnector(observability.ReporterOptions{
		DSN: "http://k@" + host + "/1", Environment: "e", SampleRate: 0, Release: "r",
		Reportable: func(error) bool { return true },
	})
	if err != nil || !r.Enabled() {
		t.Fatalf("%v enabled=%v", err, r.Enabled())
	}
}

func TestReporterConcurrentSwap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://k@" + host + "/1"

	r, err := observability.NewReporter(observability.ReporterOptions{
		Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_ = r.ApplyConnector(observability.ReporterOptions{DSN: dsn, SampleRate: 1})
				r.CaptureError(context.Background(), errors.New("x"), nil)
				if j%5 == 0 {
					r.ClearConnector()
				}
			}
		}()
	}
	wg.Wait()
	r.Flush(time.Second)
}
