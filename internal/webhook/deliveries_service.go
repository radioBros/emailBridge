package webhook

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ListDeliveries returns the caller's delivery history.
func (s *Service) ListDeliveries(ctx context.Context, filter DeliveryFilter) (*DeliveryList, error) {
	if err := tenancy.RequireRead(ctx, "deliveries"); err != nil {
		return nil, err
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && filter.From.After(filter.To) {
		return nil, errs.New(errs.CodeValidationFailed, "from must not be after to.")
	}
	dIDs, dLim := tenancy.ScopedIDs(ctx, "deliveries")
	wIDs, wLim := tenancy.ScopedIDs(ctx, "webhooks")
	if dLim && wLim {
		filter.EndpointIDs = unionIDs(dIDs, wIDs)
	}
	return s.Repo.listDeliveries(ctx, filter)
}

// GetDelivery returns one tenant-scoped delivery.
func (s *Service) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	if err := tenancy.RequireRead(ctx, "deliveries"); err != nil {
		return nil, err
	}
	d, err := s.Repo.delivery(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := tenancy.RequireReadOn(ctx, "deliveries", d.EndpointID); err != nil {
		return nil, err
	}
	return d, nil
}

// TestEndpoint sends one synthetic endpoint.test event synchronously and stores the result.
func (s *Service) TestEndpoint(ctx context.Context, endpointID string) (*DeliveryAttempt, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, endpointID); err != nil {
		return nil, err
	}
	endpoint, err := s.Repo.endpoint(ctx, endpointID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	event, err := events.Build(ctx, events.Draft{
		Type: events.EndpointTest, OrganizationID: endpoint.OrganizationID, Subject: endpoint.ID, Time: now,
		Data: &eventtypes.EndpointTest{EndpointID: endpoint.ID, Message: "EmailBridge endpoint test", SentAt: now},
	})
	if err != nil {
		return nil, err
	}
	delivery := &Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: endpoint.OrganizationID,
		EventID: event.ID, EventType: event.Type, EndpointID: endpoint.ID,
		State: StatePending, Attempts: []DeliveryAttempt{},
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(s.retention()), Version: 1,
	}
	if _, err := s.Repo.insertDelivery(ctx, delivery); err != nil {
		return nil, err
	}
	current, allowed, _, err := s.breakerAllow(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	claimed, ok, err := s.Repo.claimDelivery(ctx, delivery.ID, false, now)
	if err != nil || !ok {
		return nil, err
	}
	var outcome attemptOutcome
	switch {
	case !current.Enabled:
		outcome.Attempt = DeliveryAttempt{Kind: string(errs.KindPermanent), Error: "endpoint_disabled"}
	case !allowed:
		outcome.Attempt = DeliveryAttempt{Kind: string(errs.KindTemporary), Error: string(errs.CodeWebhookBreakerOpen)}
	default:
		outcome, err = s.attemptEvent(ctx, claimed, current, event)
		if err != nil {
			return nil, err
		}
	}
	outcome.Attempt.At = s.now()
	state := StateFailed
	if outcome.Success {
		state = StateSucceeded
	}
	if err := s.Repo.saveAttempt(ctx, claimed, outcome.Attempt, state, nil); err != nil {
		return nil, err
	}
	if allowed && current.Enabled {
		if err := s.breakerRecord(ctx, current, outcome.Success, outcome.Disable, outcome.Attempt.At); err != nil {
			return nil, err
		}
	}
	s.logAttempt(ctx, claimed, current, outcome, 1, state, nil)
	s.auditDelivery(ctx, "webhookEndpoint.test", delivery.ID, audit.Changes{
		"endpointId": {After: endpoint.ID}, "state": {After: state},
	})
	return &outcome.Attempt, nil
}

func (s *Service) publishDelivery(ctx context.Context, delivery *Delivery) error {
	if s.Publisher == nil {
		return errors.New("webhook: publisher not configured")
	}
	raw, err := json.Marshal(deliveryJob{DeliveryID: delivery.ID})
	if err != nil {
		return err
	}
	partition := bus.DeliveryPartition(delivery.EndpointID, delivery.AccountID, s.partitions())
	_, err = s.Publisher.Publish(ctx, bus.DeliverySubject(partition), raw, delivery.ID, nil)
	return err
}

func (s *Service) auditDelivery(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "delivery", ID: id}, changes); err != nil {
		s.log().WarnContext(ctx, "delivery audit write failed", "action", action, "deliveryId", id, "err", err)
	}
}

func unionIDs(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, id := range append(append([]string{}, a...), b...) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
