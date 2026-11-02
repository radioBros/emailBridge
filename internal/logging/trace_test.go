package logging

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func withRecorder(t *testing.T, opts ...sdktrace.TracerProviderOption) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(append(opts, sdktrace.WithSpanProcessor(rec))...))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestStartSpanContinuesStoredParent(t *testing.T) {
	rec := withRecorder(t)
	// A span already in ctx (the worker's own) must not win over the job's stored trace context.
	ctx, own := otel.Tracer("x").Start(context.Background(), "own")
	defer own.End()
	ctx, span := StartSpan(ctx, "webhook.deliver", parent, trace.SpanKindClient)
	span.End()

	got := rec.Ended()
	if len(got) != 1 {
		t.Fatalf("ended spans = %d", len(got))
	}
	if g := got[0].SpanContext().TraceID().String(); g != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %s", g)
	}
	if g := got[0].Parent().SpanID().String(); g != "00f067aa0ba902b7" {
		t.Fatalf("parent span = %s", g)
	}
	if got[0].SpanKind() != trace.SpanKindClient || got[0].Name() != "webhook.deliver" {
		t.Fatalf("span = %s %v", got[0].Name(), got[0].SpanKind())
	}
	ids := IDsFrom(ctx)
	if ids.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || ids.SpanID != got[0].SpanContext().SpanID().String() {
		t.Fatalf("log ids = %+v", ids)
	}
	if tp := TraceParent(ctx); TraceIDOf(tp) != ids.TraceID {
		t.Fatalf("traceparent %q does not carry the trace", tp)
	}
}

func TestStartSpanWithoutParent(t *testing.T) {
	withRecorder(t, sdktrace.WithSampler(sdktrace.NeverSample()))
	// Bad or missing parents start a new trace; an unsampled provider still yields valid IDs (S12 §10:
	// trace context is generated even with export off).
	for _, p := range []string{"", "garbage"} {
		ctx, span := StartSpan(context.Background(), "sync.fetch", p, trace.SpanKindInternal)
		span.End()
		if !span.SpanContext().IsValid() || IDsFrom(ctx).TraceID == "" {
			t.Fatalf("parent %q: no valid span context", p)
		}
		if TraceParent(ctx) == "" {
			t.Fatalf("parent %q: empty traceparent", p)
		}
	}
}

func TestNoopProviderLeavesIDsAlone(t *testing.T) {
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(noop.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	ctx, span := StartSpan(context.Background(), "x", "", trace.SpanKindInternal)
	span.End()
	if IDsFrom(ctx).TraceID != "" || TraceParent(ctx) != "" {
		t.Fatal("a noop span must not invent IDs")
	}
	if TraceIDOf("nope") != "" {
		t.Fatal("TraceIDOf of garbage")
	}
}
