package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// HandleDelivery dispatches one queued delivery. redelivered permits reclaiming a stale delivering state
// after JetStream's AckWait, giving crash recovery with at most one extra attempt.
func (s *Service) HandleDelivery(ctx context.Context, id string, redelivered bool) error {
	release, err := s.acquireGlobal(ctx)
	if err != nil {
		return err
	}
	defer release()
	delivery, err := s.Repo.deliveryForWorker(ctx, id)
	if err != nil {
		return err
	}
	if isFinal(delivery.State) {
		return nil
	}
	endpoint, err := s.Repo.endpointForWorker(ctx, delivery.OrganizationID, delivery.EndpointID)
	if err != nil {
		return err
	}
	s.schedulerOnce.Do(func() {
		if s.scheduler == nil {
			s.scheduler = newScheduler()
		}
	})
	return s.scheduler.run(
		ctx,
		endpoint.ID,
		delivery.AccountID,
		endpoint.Delivery.Ordering,
		endpoint.Delivery.MaxConcurrency,
		func() error { return s.deliverOne(ctx, delivery, endpoint, redelivered) },
	)
}

func (s *Service) acquireGlobal(ctx context.Context) (func(), error) {
	s.globalOnce.Do(func() {
		limit := s.Concurrency
		if limit <= 0 {
			limit = 64
		}
		s.globalSem = make(chan struct{}, limit)
	})
	select {
	case s.globalSem <- struct{}{}:
		return func() { <-s.globalSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) deliverOne(ctx context.Context, delivery *Delivery, endpoint *Endpoint, redelivered bool) error {
	now := s.now()
	if delivery.State == StateRetrying && delivery.NextAttemptAt != nil && now.Before(*delivery.NextAttemptAt) {
		return bus.DelayRetry(delivery.NextAttemptAt.Sub(now), errors.New("webhook: delivery retry is not due"))
	}
	if endpoint.Delivery.Ordering == OrderingStrict {
		earlier, err := s.Repo.hasEarlierUnfinished(ctx, delivery)
		if err != nil {
			return err
		}
		if earlier {
			return bus.DelayRetry(time.Second, errors.New("webhook: waiting for an earlier delivery"))
		}
	}
	endpoint, allowed, wait, err := s.breakerAllow(ctx, endpoint)
	if err != nil {
		return err
	}
	if !allowed {
		return bus.DelayRetry(max(wait, time.Second), errs.New(errs.CodeWebhookBreakerOpen, "Webhook circuit breaker is open."))
	}
	claimed, ok, err := s.Repo.claimDelivery(ctx, delivery.ID, redelivered, now)
	if err != nil || !ok {
		if releaseErr := s.releaseBreakerProbe(ctx, endpoint); releaseErr != nil {
			return errors.Join(err, releaseErr)
		}
		return err
	}
	if !endpoint.Enabled {
		attempt := DeliveryAttempt{
			At: now, Kind: string(errs.KindPermanent), Error: "endpoint_disabled",
		}
		return s.Repo.saveAttempt(ctx, claimed, attempt, StateFailed, nil)
	}

	outcome, err := s.attempt(ctx, claimed, endpoint)
	if err != nil {
		return err
	}
	finished := s.now()
	outcome.Attempt.At = finished
	attemptNumber := len(claimed.Attempts) + 1
	state := StateFailed
	var next *time.Time
	if outcome.Success {
		state = StateSucceeded
	} else if outcome.Retry && attemptNumber < endpoint.Retry.MaxAttempts {
		state = StateRetrying
		delay := outcome.RetryAfter
		if delay <= 0 {
			delay = retryDelay(
				attemptNumber,
				parseDuration(endpoint.Retry.BackoffBase, 5*time.Second),
				endpoint.Retry.BackoffJitter,
				s.random(),
			)
		}
		value := finished.Add(delay)
		next = &value
	} else if outcome.Retry {
		state = StateDeadLettered
		if s.Metrics != nil {
			s.Metrics.DeadLetter(QueueDelivery)
		}
	}
	if err := s.Repo.saveAttempt(ctx, claimed, outcome.Attempt, state, next); err != nil {
		return err
	}
	if err := s.breakerRecord(ctx, endpoint, outcome.Success, outcome.Disable, finished); err != nil {
		return err
	}
	s.logAttempt(ctx, claimed, endpoint, outcome, attemptNumber, state, next)
	if next != nil {
		return bus.DelayRetry(next.Sub(finished), errors.New("webhook: delivery retry scheduled"))
	}
	return nil
}

func (s *Service) attempt(ctx context.Context, delivery *Delivery, endpoint *Endpoint) (attemptOutcome, error) {
	eventCtx := store.WithOrganization(ctx, delivery.OrganizationID)
	event, err := s.Events.Get(eventCtx, delivery.EventID)
	if err != nil {
		return attemptOutcome{}, err
	}
	return s.attemptEvent(ctx, delivery, endpoint, event)
}

func (s *Service) attemptEvent(
	ctx context.Context,
	delivery *Delivery,
	endpoint *Endpoint,
	event events.Event,
) (attemptOutcome, error) {
	payload := endpoint.Payload
	custom, err := s.decryptHeaders(ctx, endpoint)
	if err != nil {
		return attemptOutcome{}, err
	}
	secrets, err := s.decryptSecrets(ctx, endpoint)
	if err != nil {
		return attemptOutcome{}, err
	}
	if delivery.RouteID != "" {
		route, err := s.Repo.routeForWorker(ctx, delivery.OrganizationID, delivery.RouteID)
		if err != nil {
			return attemptOutcome{}, err
		}
		payload = payloadOverride(payload, route.Payload)
		for name, value := range routeHeaderValues(route.Headers) {
			custom[name] = value
		}
		if !route.Secret.IsZero() {
			plain, err := s.Cipher.DecryptString(ctx, route.Secret, crypto.AAD{
				Collection: rules.RoutesCollection, Field: "secret", DocID: route.ID,
			})
			if err != nil {
				return attemptOutcome{}, err
			}
			secrets = []string{plain}
		}
	}
	var body []byte
	mappedBody, hasMapped, err := s.openMappedBody(ctx, delivery)
	if err != nil {
		return attemptOutcome{}, err
	}
	if hasMapped {
		body = []byte(mappedBody)
	} else {
		body, err = buildBody(event, nil, false, payload)
		if err != nil {
			return attemptOutcome{}, err
		}
	}
	if endpoint.Kind != KindHTTP && endpoint.Kind != "" {
		return s.sendSink(ctx, delivery, endpoint, event, body, custom)
	}
	return s.sendRequest(ctx, delivery, endpoint, event, body, custom, secrets)
}

func (s *Service) sendRequest(
	ctx context.Context,
	delivery *Delivery,
	endpoint *Endpoint,
	event events.Event,
	body []byte,
	custom map[string]string,
	secrets []string,
) (attemptOutcome, error) {
	ctx, span := logging.StartSpan(ctx, "webhook.deliver", event.TraceParent, trace.SpanKindClient,
		attribute.String("eb.event.type", event.Type), attribute.Int("eb.attempt", len(delivery.Attempts)+1))
	defer span.End()
	outcome, err := s.sendRequestIn(ctx, delivery, endpoint, event, body, custom, secrets)
	switch {
	case err != nil:
		span.SetStatus(codes.Error, "attempt failed")
	case outcome.Attempt.StatusCode > 0:
		span.SetAttributes(attribute.Int("http.response.status_code", outcome.Attempt.StatusCode))
		if !outcome.Success {
			span.SetStatus(codes.Error, "delivery not accepted")
		}
	case !outcome.Success:
		span.SetStatus(codes.Error, "no response")
	}
	return outcome, err
}

func (s *Service) sendRequestIn(
	ctx context.Context,
	delivery *Delivery,
	endpoint *Endpoint,
	event events.Event,
	body []byte,
	custom map[string]string,
	secrets []string,
) (attemptOutcome, error) {
	timeout := parseDuration(endpoint.Retry.Timeout, 30*time.Second)
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return attemptOutcome{}, err
	}
	at := s.now()
	signature, err := SignatureHeaders(s.signatureMode(ctx, endpoint), delivery.EventID, at, body, secrets)
	if err != nil {
		return attemptOutcome{}, err
	}
	req.Header = signature
	contentType := "application/json"
	if endpoint.Delivery.ContentType == "cloudevents" {
		contentType = "application/cloudevents+json"
	}
	req.Header.Set("Content-Type", contentType)
	version := s.Version
	if version == "" {
		version = "dev"
	}
	req.Header.Set("User-Agent", "EmailBridge/"+version)
	req.Header.Set("eb-event-type", delivery.EventType)
	req.Header.Set("eb-delivery-id", delivery.ID)
	req.Header.Set("eb-attempt", strconv.Itoa(len(delivery.Attempts)+1))
	if delivery.ReplayOf != "" {
		req.Header.Set("eb-replay", "true")
	}
	if tp := logging.TraceParent(ctx); tp != "" { // the attempt's own span, a child of the event's trace
		req.Header.Set("traceparent", tp)
	} else if event.TraceParent != "" {
		req.Header.Set("traceparent", event.TraceParent)
	}
	if err := addCustomHeaders(req.Header, custom); err != nil {
		return attemptOutcome{}, err
	}

	client, err := s.httpClientFor(endpoint, timeout)
	if err != nil {
		return attemptOutcome{}, err
	}
	started := time.Now()
	response, requestErr := client.Do(req)
	latency := time.Since(started)
	if requestErr != nil {
		if errors.Is(requestErr, egress.ErrBlocked) {
			s.securityLog().WarnContext(ctx, "webhook delivery egress blocked",
				"endpointId", endpoint.ID,
				"host", req.URL.Hostname(),
				"code", errs.CodeEndpointURLBlocked,
			)
		}
		out := classifyRequestError(requestErr)
		out.Attempt.LatencyMs = latency.Milliseconds()
		return out, nil
	}
	defer func() { _ = response.Body.Close() }()
	out := classifyResponse(response.StatusCode, endpoint.Delivery.RetryOn4xx, response.Header, s.now())
	out.Attempt.LatencyMs = latency.Milliseconds()
	limit := s.ResponseExcerpt
	if limit <= 0 {
		limit = 4 << 10
	}
	excerpt, err := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if err != nil {
		return attemptOutcome{}, err
	}
	if len(excerpt) > limit {
		excerpt = excerpt[:limit]
	}
	out.Attempt.ResponseExcerpt = strings.ToValidUTF8(string(excerpt), "�")
	out.Attempt.ResponseHeaders = selectedResponseHeaders(response.Header)
	return out, nil
}

// signatureMode resolves webhook.signature.mode global → organisation → endpoint (W51 / S9).
// The endpoint layer is the feature switch on the endpoint; Security.SignatureMode is used only when
// no higher/lower switch value won (catalogue default), so a default "standard" on the document does
// not mask an organisation or global setting.
func (s *Service) signatureMode(ctx context.Context, endpoint *Endpoint) string {
	if endpoint == nil {
		return SignatureStandard
	}
	reg := features.DefaultRegistry()
	if s.Settings != nil && s.Settings.Registry != nil {
		reg = s.Settings.Registry
	}
	sw, ok := reg.Get("webhook.signature.mode")
	if !ok {
		return fallbackSignatureMode(endpoint)
	}
	vals := features.Values{}
	if mode, ok := endpoint.Switches["webhook.signature.mode"].(string); ok && mode != "" {
		vals.Lower = &features.Setting{Value: features.StringValue(mode)}
	}
	env := "production"
	if s.Settings != nil {
		if s.Settings.EnvName != "" {
			env = s.Settings.EnvName
		}
		if s.Settings.DB != nil {
			read := func(docID string) *features.Setting {
				var doc settings.Doc
				err := s.Settings.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
				if err != nil || doc.Switches == nil {
					return nil
				}
				sv, ok := doc.Switches["webhook.signature.mode"]
				if !ok {
					return nil
				}
				str, ok := sv.Value.(string)
				if !ok || str == "" {
					return nil
				}
				return &features.Setting{Value: features.StringValue(str), Locked: sv.Locked}
			}
			if features.Allows(sw, features.ScopeGlobal) {
				vals.Global = read(settings.DocIDGlobal)
			}
			if endpoint.OrganizationID != "" && features.Allows(sw, features.ScopeOrganization) {
				vals.Organization = read(settings.OrgDocID(endpoint.OrganizationID))
			}
		}
	}
	v, src := features.Resolve(sw, env, vals)
	if src != features.SourceDefault && v.String != nil && *v.String != "" {
		return *v.String
	}
	return fallbackSignatureMode(endpoint)
}

func fallbackSignatureMode(endpoint *Endpoint) string {
	if endpoint == nil {
		return SignatureStandard
	}
	if mode, ok := endpoint.Switches["webhook.signature.mode"].(string); ok && mode != "" {
		return mode
	}
	if endpoint.Security.SignatureMode != "" {
		return endpoint.Security.SignatureMode
	}
	return SignatureStandard
}

func selectedResponseHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for _, name := range []string{"Retry-After", "Content-Type", "X-Request-Id"} {
		if value := headers.Get(name); value != "" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func routeHeaderValues(raw map[string]any) map[string]string {
	out := map[string]string{}
	for name, value := range raw {
		switch typed := value.(type) {
		case string:
			out[name] = typed
		case map[string]any:
			if text, ok := typed["value"].(string); ok {
				out[name] = text
			}
		case bson.M:
			if text, ok := typed["value"].(string); ok {
				out[name] = text
			}
		case bson.D:
			for _, item := range typed {
				if item.Key == "value" {
					if text, ok := item.Value.(string); ok {
						out[name] = text
					}
					break
				}
			}
		}
	}
	return out
}

func (s *Service) breakerAllow(ctx context.Context, endpoint *Endpoint) (*Endpoint, bool, time.Duration, error) {
	// Fast path: a closed breaker allows every attempt and Allow changes nothing, so the endpoint loaded for
	// this delivery is enough. Serialising a re-read per delivery under the endpoint lock capped one endpoint
	// at ~1 / (MongoDB round trip) deliveries per second (P2.4 load run).
	if endpoint.Breaker.State == "" || endpoint.Breaker.State == "closed" {
		return endpoint, true, 0, nil
	}
	lock := s.breakerLock(endpoint.ID)
	lock.Lock()
	defer lock.Unlock()
	for {
		current, err := s.Repo.endpointForWorker(ctx, endpoint.OrganizationID, endpoint.ID)
		if err != nil {
			return nil, false, 0, err
		}
		beforeState, beforeProbe := current.Breaker.State, current.Breaker.ProbeInFlight
		allowed, wait := current.Breaker.Allow(s.now())
		if current.Breaker.State == beforeState && current.Breaker.ProbeInFlight == beforeProbe {
			return current, allowed, wait, nil
		}
		if err := s.Repo.saveBreaker(ctx, current); err != nil {
			if errs.CodeOf(err) == errs.CodeConflict {
				continue
			}
			return nil, false, 0, err
		}
		return current, allowed, wait, nil
	}
}

func (s *Service) breakerRecord(ctx context.Context, endpoint *Endpoint, success, disable bool, now time.Time) error {
	// Fast path: a success on a healthy breaker (closed, no failures in the window, no failure streak) stores
	// nothing new — skip the locked read-modify-write of the endpoint that every delivery used to pay. Any
	// failure takes the full path, and the next success after it is recorded normally.
	if success && endpoint.FailureStartedAt == nil && endpoint.Breaker.healthy() {
		return nil
	}
	lock := s.breakerLock(endpoint.ID)
	lock.Lock()
	defer lock.Unlock()
	for {
		current, err := s.Repo.endpointForWorker(ctx, endpoint.OrganizationID, endpoint.ID)
		if err != nil {
			return err
		}
		wasEnabled := current.Enabled
		current.Breaker.Record(now, success)
		if success {
			current.FailureStartedAt = nil
		} else {
			if current.FailureStartedAt == nil {
				started := now
				current.FailureStartedAt = &started
			}
			autoDisable := parseDuration(current.Delivery.AutoDisableAfter, 5*24*time.Hour)
			if disable || now.Sub(*current.FailureStartedAt) >= autoDisable {
				current.Enabled = false
				code := "continuous_failure"
				if disable {
					code = "http_410"
				}
				current.DisabledReason = &DisabledReason{Code: code, At: now}
			}
		}
		if err := s.Repo.saveBreaker(ctx, current); err != nil {
			if errs.CodeOf(err) == errs.CodeConflict {
				continue
			}
			return err
		}
		if wasEnabled && !current.Enabled && s.NotifyDisabled != nil {
			s.NotifyDisabled(ctx, current)
		}
		return nil
	}
}

func (s *Service) releaseBreakerProbe(ctx context.Context, endpoint *Endpoint) error {
	lock := s.breakerLock(endpoint.ID)
	lock.Lock()
	defer lock.Unlock()
	for {
		current, err := s.Repo.endpointForWorker(ctx, endpoint.OrganizationID, endpoint.ID)
		if err != nil {
			return err
		}
		if current.Breaker.State != "halfOpen" || !current.Breaker.ProbeInFlight {
			return nil
		}
		current.Breaker.ProbeInFlight = false
		if err := s.Repo.saveBreaker(ctx, current); err != nil {
			if errs.CodeOf(err) == errs.CodeConflict {
				continue
			}
			return err
		}
		return nil
	}
}

func (s *Service) breakerLock(endpointID string) *sync.Mutex {
	value, _ := s.breakerMu.LoadOrStore(endpointID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *Service) logAttempt(
	ctx context.Context,
	delivery *Delivery,
	endpoint *Endpoint,
	outcome attemptOutcome,
	attempt int,
	state string,
	next *time.Time,
) {
	logCtx := logging.With(ctx, func(ids *logging.IDs) {
		ids.OrganizationID, ids.AccountID = delivery.OrganizationID, delivery.AccountID
		ids.EventID, ids.DeliveryID = delivery.EventID, delivery.ID
	})
	attrs := []any{
		"endpointId", endpoint.ID,
		"eventType", delivery.EventType,
		"attempt", attempt,
		"state", state,
		"status", outcome.Attempt.StatusCode,
		"latencyMs", outcome.Attempt.LatencyMs,
		"kind", outcome.Attempt.Kind,
		"code", outcome.Attempt.Error,
	}
	if next != nil {
		attrs = append(attrs, "nextRetryAt", *next)
	}
	if outcome.Success {
		s.log().InfoContext(logCtx, "webhook delivery succeeded", attrs...)
	} else {
		s.log().WarnContext(logCtx, "webhook delivery attempt failed", attrs...)
	}
}

func decodeJob(data []byte) (deliveryJob, error) {
	var job deliveryJob
	if err := json.Unmarshal(data, &job); err != nil {
		return deliveryJob{}, err
	}
	if job.DeliveryID == "" {
		return deliveryJob{}, fmt.Errorf("webhook: deliveryId is required")
	}
	return job, nil
}
