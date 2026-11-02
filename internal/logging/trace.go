package logging

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the instrumentation scope of every span EmailBridge starts outside the HTTP layer.
const TracerName = "emailbridge"

// StartSpan starts a span named name (S12 §10). When parent is a W3C traceparent — the stored trace context of
// an event or an outbound message — the span continues that trace, even if ctx carries a span of its own:
// background workers start from a job, and the job's trace is the one that matters. The trace and span IDs
// are stamped on the log context, so every record logged under the returned ctx carries them.
func StartSpan(ctx context.Context, name, parent string, kind trace.SpanKind, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if parent != "" {
		if sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(),
			propagation.MapCarrier{"traceparent": parent})); sc.IsValid() {
			ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
		}
	}
	ctx, span := otel.Tracer(TracerName).Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
	if sc := span.SpanContext(); sc.IsValid() {
		ctx = With(ctx, func(i *IDs) { i.TraceID, i.SpanID = sc.TraceID().String(), sc.SpanID().String() })
	}
	return ctx, span
}

// TraceParent is the W3C traceparent of the span in ctx, "" without a valid one.
func TraceParent(ctx context.Context) string {
	c := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, c)
	return c.Get("traceparent")
}

// TraceIDOf is the trace ID of a W3C traceparent, "" when it does not parse.
func TraceIDOf(traceparent string) string {
	sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(),
		propagation.MapCarrier{"traceparent": traceparent}))
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
