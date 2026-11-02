// Package observability provides Prometheus metrics, OpenTelemetry tracing and a Sentry-compatible
// error reporter for EmailBridge (S12 §9–10, §12).
//
// OTel's global propagator and tracer provider are the accepted globals (documented here); the Sentry
// Reporter uses its own Client and Hub and never touches the package-level hub.
package observability
