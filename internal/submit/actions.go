package submit

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Retry re-queues a failed or outcome-unknown message for another attempt (P5.3).
func (s *Service) Retry(ctx context.Context, id, ifMatch string) (*Message, error) {
	if _, ok := tenancy.CallerFrom(ctx); !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	m, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Per-message check (like Cancel): an account-bound or account-scoped key acts on its own account only.
	if err := tenancy.RequireAccount(ctx, tenancy.PermSend, m.AccountID); err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, m.Version); err != nil {
		return nil, err
	}
	switch m.State {
	case StateQueued:
		if m.NextAttemptAt == nil || !m.NextAttemptAt.After(s.now()) {
			return m, nil // already due
		}
	case StateFailed, StateOutcomeUnknown:
	default:
		return nil, errs.New(errs.CodeOutboundNotRetryable, "Only failed or outcome-unknown messages can be retried.").
			WithSafe("state", m.State)
	}
	now := s.now()
	res, err := s.Repo.coll().UpdateOne(ctx,
		bson.M{"_id": m.ID, "organizationId": m.OrganizationID, "version": m.Version,
			"state": bson.M{"$in": []string{StateFailed, StateOutcomeUnknown, StateQueued}}},
		bson.M{
			"$set":   bson.M{"state": StateQueued, "nextAttemptAt": now},
			"$unset": bson.M{"finalError": "", "worker": "", "heartbeatAt": ""},
			"$inc":   bson.M{"version": 1},
		})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.retry")
	}
	if res.MatchedCount == 0 {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	m, err = s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, m, len(m.Attempts))
	s.auditOutbound(ctx, "outbound.retried", m.ID, audit.Changes{"state": {After: StateQueued}})
	return m, nil
}

// SendNow makes a scheduled or delayed message due immediately (P5.3).
func (s *Service) SendNow(ctx context.Context, id, ifMatch string) (*Message, error) {
	if _, ok := tenancy.CallerFrom(ctx); !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	m, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Per-message check (like Cancel): an account-bound or account-scoped key acts on its own account only.
	if err := tenancy.RequireAccount(ctx, tenancy.PermSend, m.AccountID); err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, m.Version); err != nil {
		return nil, err
	}
	now := s.now()
	switch m.State {
	case StateQueued:
		if m.NextAttemptAt == nil || !m.NextAttemptAt.After(now) {
			return m, nil
		}
	case StateScheduled:
	default:
		return nil, errs.New(errs.CodeOutboundNotSendable, "Only scheduled or delayed queued messages can be sent now.").
			WithSafe("state", m.State)
	}
	set := bson.M{"state": StateQueued, "nextAttemptAt": now}
	if m.State == StateScheduled {
		set["sendAt"] = now
	}
	res, err := s.Repo.coll().UpdateOne(ctx,
		bson.M{"_id": m.ID, "organizationId": m.OrganizationID, "version": m.Version,
			"state": bson.M{"$in": []string{StateScheduled, StateQueued}}},
		bson.M{"$set": set, "$inc": bson.M{"version": 1}})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.send_now")
	}
	if res.MatchedCount == 0 {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	m, err = s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, m, len(m.Attempts))
	s.auditOutbound(ctx, "outbound.send_now", m.ID, audit.Changes{"state": {After: StateQueued}})
	return m, nil
}

func (s *Service) auditOutbound(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "outboundMessage", ID: id}, changes)
}
