package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// TracingOptions configure OpenTelemetry export (EB_OTEL_ENABLED + OTEL_EXPORTER_OTLP_*).
type TracingOptions struct {
	Enabled     bool
	ServiceName string
	Version     string
	NodeID      string
}

// InitTracing installs the W3C traceparent propagator always (S12 §10). With export off it installs a
// provider that samples nothing: spans record nothing and export nothing, but they still carry valid trace
// and span IDs, so trace context is generated and propagated (events, webhooks, logs) either way. When
// Enabled it configures an OTLP gRPC exporter (standard OTEL_EXPORTER_OTLP_* and OTEL_TRACES_SAMPLER env).
// Init must not block startup.
func InitTracing(ctx context.Context, o TracingOptions) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !o.Enabled {
		tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
		otel.SetTracerProvider(tp)
		return tp.Shutdown, nil
	}
	if o.ServiceName == "" {
		o.ServiceName = "emailbridge"
	}
	expCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	exporter, err := otlptracegrpc.New(expCtx)
	if err != nil {
		return nil, fmt.Errorf("observability: otlp exporter: %w", err)
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(o.ServiceName),
			semconv.ServiceVersion(o.Version),
			semconv.ServiceInstanceID(o.NodeID),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(context.Background())
		return nil, fmt.Errorf("observability: resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
