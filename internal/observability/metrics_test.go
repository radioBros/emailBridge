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

	"github.com/prometheus/client_golang/prometheus"

	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/observability"
)

func TestMetricsEndpointAndLogCounters(t *testing.T) {
	sys := logging.New(logging.Options{Node: "n1", Role: "api", Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(context.Background()) }()
	log := sys.For(logging.CatSystem, "test")
	log.Info("hello metrics")
	_ = sys.Flush(context.Background())

	m := observability.NewMetrics()
	if err := m.ObserveLogs(sys); err != nil {
		t.Fatal(err)
	}
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

	var body string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		body = string(b)
		if strings.Contains(body, "eb_log_records_total") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "eb_log_records_total") {
		t.Fatalf("missing eb_log_records_total in %q", body)
	}
	if !strings.Contains(body, `category="system"`) {
		t.Fatalf("missing category label: %s", body)
	}
}

func TestMetricsDisabledAddr(t *testing.T) {
	m := observability.NewMetrics()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = m.Serve(ctx, "")
	if ctx.Err() == nil {
		t.Fatal("expected context to cancel")
	}
}

func TestMetricsRegister(t *testing.T) {
	m := observability.NewMetrics()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "eb_test_gauge"})
	if err := m.Register(g); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(g); err == nil {
		t.Fatal("expected duplicate register error")
	}
}
