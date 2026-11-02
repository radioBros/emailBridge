package observability_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/observability"
)

func TestModuleWithMetricsAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	sys := logging.New(logging.Options{Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(context.Background()) }()
	m := &observability.Module{Options: observability.ModuleOptions{
		MetricsAddr: addr,
		LogSystem:   sys,
	}}
	a := app.New(app.Options{NodeID: "n1", Modules: []app.Module{m}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestObserveLogsNil(t *testing.T) {
	m := observability.NewMetrics()
	if err := m.ObserveLogs(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestMetricsShutdownIdle(t *testing.T) {
	m := observability.NewMetrics()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
