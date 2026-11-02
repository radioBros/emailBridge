package evidence_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/jobs"
)

func TestModuleStart(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n1", Log: quiet})
	m := &evidence.Module{
		Options: evidence.Options{Backend: evidence.NewMemoryBackend(true), Retention: time.Hour},
		Log:     quiet,
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Store() == nil || !m.Store().Ready() {
		t.Fatal("store not ready")
	}
	_ = m.Stop(context.Background())

	a2 := app.New(app.Options{NodeID: "n2", Log: quiet})
	app.Provide(a2, &jobs.Service{Log: quiet})
	m2 := &evidence.Module{Options: evidence.Options{Backend: evidence.NewMemoryBackend(true)}, Log: quiet}
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
	_ = m2.Stop(context.Background())
}

func TestModuleNoBucket(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n1", Log: quiet})
	m := &evidence.Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Store() != nil {
		t.Fatal("expected nil store")
	}
}

func TestOptionsFromEnv(t *testing.T) {
	o := evidence.OptionsFromEnv("http://127.0.0.1:9000", "us-east-1", "a", "s", "ev", true, "COMPLIANCE", time.Hour, nil, nil)
	if o.Bucket != "ev" || o.LockMode != "COMPLIANCE" {
		t.Fatalf("%+v", o)
	}
}
