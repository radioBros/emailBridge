package observability_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/observability"
)

func TestModuleStartStop(t *testing.T) {
	sys := logging.New(logging.Options{Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(context.Background()) }()
	m := &observability.Module{Options: observability.ModuleOptions{
		MetricsAddr: "",
		Tracing:     observability.TracingOptions{Enabled: false},
		Reporter:    observability.ReporterOptions{},
		LogSystem:   sys,
	}}
	a := app.New(app.Options{NodeID: "n1", Modules: []app.Module{m}})
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	metrics, err := app.MustGet[*observability.Metrics](a)
	if err != nil || metrics == nil {
		t.Fatalf("%v", err)
	}
	rep, err := app.MustGet[*observability.Reporter](a)
	if err != nil || rep == nil {
		t.Fatalf("%v", err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := a.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "observability" || m.Roles() != nil {
		t.Fatal("module meta")
	}
}
