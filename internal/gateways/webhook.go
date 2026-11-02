package gateways

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

const maxWebhookBody = 1 << 20 // 1 MiB

// DeliverySink receives verified provider delivery events (wired by Claude to outbound + suppression).
type DeliverySink interface {
	Deliver(ctx context.Context, orgID, gatewayID string, events []DeliveryEvent) error
}

// HandleDeliveryWebhook serves POST /callbacks/gateways/{gatewayId}.
func (s *Service) HandleDeliveryWebhook(ctx context.Context, gatewayID string, r *http.Request) error {
	if s.limiter == nil {
		s.limiter = newCallbackLimiter(60)
	}
	now := time.Now().UTC()
	if err := s.limiter.take(gatewayID, now); err != nil {
		return err
	}
	if !store.IsID(store.PrefixGateway, gatewayID) {
		return errs.New(errs.CodeNotFound, "Not found.")
	}
	g, err := s.Repo.getByIDAny(ctx, gatewayID)
	if err != nil {
		return errs.New(errs.CodeNotFound, "Not found.")
	}
	if !g.IsAPI() || !g.Enabled {
		return errs.New(errs.CodeNotFound, "Not found.")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "Could not read body.")
	}
	if len(body) > maxWebhookBody {
		return errs.New(errs.CodePayloadTooLarge, "Request body too large.")
	}
	secret, err := s.decryptWebhookSecret(ctx, g)
	if err != nil {
		return errs.New(errs.CodeNotFound, "Not found.")
	}
	kind := g.EffectiveKind()
	ok, confirmURL, verr := verifyProviderSignature(kind, secret, r.Header, r.URL.Query().Get("token"), body, now)
	if verr != nil {
		return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
	}
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
	}
	if kind == KindSES {
		if err := VerifySNSMessage(ctx, s.httpClient(30*time.Second), body); err != nil {
			return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
		}
	}
	if confirmURL != "" {
		return s.confirmSNS(ctx, confirmURL)
	}
	p, okp := ProviderFor(kind)
	if !okp {
		return errs.New(errs.CodeNotFound, "Not found.")
	}
	payload := body
	if kind == KindSES {
		inner, isConfirm, cerr := unwrapSNS(body)
		if cerr != nil {
			return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
		}
		if isConfirm {
			return nil
		}
		payload = inner
	}
	events, err := p.ParseWebhook(r.Header, payload)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "Invalid webhook body.")
	}
	if len(events) == 0 {
		return nil
	}
	if s.Sink == nil {
		return errs.New(errs.CodeServiceUnavailable, "Delivery sink is not configured.")
	}
	return s.Sink.Deliver(ctx, g.OrganizationID, g.ID, events)
}

func (m *Module) rawGatewayWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "gatewayId")
	err := m.svc.HandleDeliveryWebhook(r.Context(), id, r)
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- rate limit (same shape as integrations) ---

type callbackLimiter struct {
	mu   sync.Mutex
	rate float64
	cap  float64
	buck map[string]*rlBucket
}

type rlBucket struct {
	tokens float64
	last   time.Time
}

func newCallbackLimiter(perMinute float64) *callbackLimiter {
	if perMinute <= 0 {
		perMinute = 60
	}
	return &callbackLimiter{
		rate: perMinute / 60, cap: perMinute, buck: map[string]*rlBucket{},
	}
}

func (l *callbackLimiter) take(key string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buck[key]
	if b == nil {
		b = &rlBucket{tokens: l.cap, last: now}
		l.buck[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.cap {
		b.tokens = l.cap
	}
	b.last = now
	if b.tokens < 1 {
		return errs.New(errs.CodeRateLimited, "Too many gateway callbacks; try again later.")
	}
	b.tokens--
	return nil
}

func bearerToken(h http.Header) string {
	a := h.Get("Authorization")
	if len(a) < 8 || !strings.EqualFold(a[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(a[7:])
}
