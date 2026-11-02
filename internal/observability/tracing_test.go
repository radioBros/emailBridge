package observability_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/radiobros/emailbridge/internal/observability"
)

func TestInitTracingDisabled(t *testing.T) {
	shutdown, err := observability.InitTracing(context.Background(), observability.TracingOptions{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInitTracingEnabledNonBlocking(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown, err := observability.InitTracing(ctx, observability.TracingOptions{
		Enabled: true, ServiceName: "emailbridge-test", Version: "dev", NodeID: "n1",
	})
	elapsed := time.Since(start)
	if err != nil {
		// exporter may fail fast on some platforms; that is still non-blocking
		if elapsed > 3*time.Second {
			t.Fatalf("blocked too long (%v): %v", elapsed, err)
		}
		return
	}
	if elapsed > 3*time.Second {
		t.Fatalf("init blocked for %v", elapsed)
	}
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = shutdown(sctx)
}

func TestInitTracingDisabledStillMintsIDs(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	shutdown, err := observability.InitTracing(context.Background(), observability.TracingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	_, span := otel.Tracer("t").Start(context.Background(), "x")
	defer span.End()
	if !span.SpanContext().IsValid() || span.IsRecording() || span.SpanContext().IsSampled() {
		t.Fatalf("export off: want valid, unsampled, non-recording span; got %+v", span.SpanContext())
	}
}
