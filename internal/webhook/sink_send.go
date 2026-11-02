package webhook

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/sinks"
)

func (s *Service) sendSink(
	ctx context.Context,
	delivery *Delivery,
	endpoint *Endpoint,
	event events.Event,
	body []byte,
	custom map[string]string,
) (attemptOutcome, error) {
	ctx, span := logging.StartSpan(ctx, "webhook.deliver", event.TraceParent, trace.SpanKindClient,
		attribute.String("eb.event.type", event.Type),
		attribute.Int("eb.attempt", len(delivery.Attempts)+1),
		attribute.String("eb.sink.kind", endpoint.Kind),
	)
	defer span.End()

	timeout := parseDuration(endpoint.Retry.Timeout, 30*time.Second)
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if endpoint.Kind == KindIntegration {
		return s.sendIntegrationAction(attemptCtx, span, delivery, endpoint, event, body, custom)
	}

	plain, err := s.decryptSinkConfig(attemptCtx, endpoint)
	if err != nil {
		return attemptOutcome{}, err
	}
	typed, err := parseTypedSink(endpoint.Kind, plain)
	if err != nil {
		return attemptOutcome{}, err
	}
	typed.EndpointID = endpoint.ID
	typed.Version = endpoint.Version

	deps := sinks.Deps{
		Dial:                    s.Egress,
		AllowAmbientCredentials: s.allowAmbientCredentials(attemptCtx),
	}
	sink, err := s.sinksCache().GetWithDeps(endpoint.Kind, typed, deps)
	if err != nil {
		out := classifySinkError(err)
		span.SetStatus(codes.Error, "sink build failed")
		return out, nil
	}

	headers := map[string]string{}
	for k, v := range custom {
		headers[k] = v
	}
	headers["ce_id"] = delivery.EventID
	headers["ce_type"] = delivery.EventType
	headers["ce_source"] = event.Source // CloudEvents binary mode: the envelope's own attributes (S3)
	headers["ce_specversion"] = "1.0"
	if !event.Time.IsZero() {
		headers["ce_time"] = event.Time.UTC().Format(time.RFC3339Nano)
	}
	if event.Subject != "" {
		headers["ce_subject"] = event.Subject
	}
	headers["eb-event-type"] = delivery.EventType
	headers["eb-delivery-id"] = delivery.ID
	headers["eb-attempt"] = strconv.Itoa(len(delivery.Attempts) + 1)
	if delivery.ReplayOf != "" {
		headers["eb-replay"] = "true"
	}
	if tp := logging.TraceParent(ctx); tp != "" {
		headers["traceparent"] = tp
	} else if event.TraceParent != "" {
		headers["traceparent"] = event.TraceParent
	}

	msg := sinks.Message{
		ID:        delivery.EventID,
		Type:      delivery.EventType,
		Subject:   event.Subject,
		AccountID: delivery.AccountID,
		Headers:   headers,
		Body:      body,
	}
	started := time.Now()
	sendErr := sink.Send(attemptCtx, msg)
	latency := time.Since(started)
	if sendErr != nil {
		out := classifySinkError(sendErr)
		out.Attempt.LatencyMs = latency.Milliseconds()
		out.Attempt.Kind = string(sink.Classify(sendErr))
		if !isTransientKind(errs.Kind(out.Attempt.Kind)) {
			out.Retry = false
		} else {
			out.Retry = true
		}
		span.SetStatus(codes.Error, "sink send failed")
		return out, nil
	}
	span.SetAttributes(attribute.Bool("eb.sink.ok", true))
	return attemptOutcome{Success: true, Attempt: DeliveryAttempt{LatencyMs: latency.Milliseconds()}}, nil
}

func (s *Service) sendIntegrationAction(
	ctx context.Context,
	span trace.Span,
	delivery *Delivery,
	endpoint *Endpoint,
	event events.Event,
	body []byte,
	custom map[string]string,
) (attemptOutcome, error) {
	if s.IntegrationAction == nil {
		out := attemptOutcome{Attempt: DeliveryAttempt{Error: "integration action not configured", Kind: string(errs.KindInternal)}}
		span.SetStatus(codes.Error, "integration action missing")
		return out, nil
	}
	instanceID, _ := endpoint.SinkConfig["instanceId"].(string)
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		out := attemptOutcome{Attempt: DeliveryAttempt{Error: "sinkConfig.instanceId missing", Kind: string(errs.KindPermanent)}}
		span.SetStatus(codes.Error, "missing instanceId")
		return out, nil
	}
	headers := map[string]string{}
	for k, v := range custom {
		headers[k] = v
	}
	headers["ce_id"] = delivery.EventID
	headers["ce_type"] = delivery.EventType
	headers["eb-event-type"] = delivery.EventType
	headers["eb-delivery-id"] = delivery.ID
	msg := sinks.Message{
		ID: delivery.EventID, Type: delivery.EventType, Subject: event.Subject,
		AccountID: delivery.AccountID, Headers: headers, Body: body,
	}
	started := time.Now()
	sendErr := s.IntegrationAction(ctx, endpoint.OrganizationID, instanceID, msg)
	latency := time.Since(started)
	if sendErr != nil {
		out := classifySinkError(sendErr)
		out.Attempt.LatencyMs = latency.Milliseconds()
		if !isTransientKind(errs.Kind(out.Attempt.Kind)) {
			out.Retry = false
		} else {
			out.Retry = true
		}
		span.SetStatus(codes.Error, "integration action failed")
		return out, nil
	}
	span.SetAttributes(attribute.Bool("eb.sink.ok", true))
	return attemptOutcome{Success: true, Attempt: DeliveryAttempt{LatencyMs: latency.Milliseconds()}}, nil
}

func classifySinkError(err error) attemptOutcome {
	kind := errs.KindOf(err)
	msg := strings.ToLower(err.Error())
	if kind == errs.KindInternal || kind == "" {
		switch {
		case strings.Contains(msg, "timeout"), strings.Contains(msg, "connection"),
			strings.Contains(msg, "refused"), strings.Contains(msg, "temporary"),
			strings.Contains(msg, "unavailable"), strings.Contains(msg, "reset"),
			strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "broken pipe"):
			kind = errs.KindTemporary
		}
	}
	out := attemptOutcome{Attempt: DeliveryAttempt{Error: err.Error(), Kind: string(kind)}}
	if isTransientKind(kind) {
		out.Retry = true
	}
	return out
}

func isTransientKind(k errs.Kind) bool {
	switch k {
	case errs.KindTemporary, errs.KindRateLimited, errs.KindOutcomeUnknown:
		return true
	default:
		return false
	}
}
