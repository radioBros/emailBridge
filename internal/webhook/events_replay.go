package webhook

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	jobKindEventsReplay     = "events.replay"
	eventsReplayRatePerSec  = 50
	eventsReplayCatchUpPage = 500
)

// eventsReplayBatchSize is the CatchUp page size (tests may lower it).
var eventsReplayBatchSize = eventsReplayCatchUpPage

// EventsReplayRequest is POST /v1/events/replay.
type EventsReplayRequest struct {
	EndpointID           string    `json:"endpointId" doc:"Endpoint to deliver to (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	From                 time.Time `json:"from" doc:"Replay stored events from this time (RFC 3339, within the event retention)" example:"2026-10-26T00:00:00Z"`
	To                   time.Time `json:"to,omitempty" doc:"Up to this time (default now)" example:"2026-10-27T00:00:00Z"`
	Types                []string  `json:"types,omitempty" doc:"Only these event types (default: the endpoint's subscription)"`
	IgnoreEndpointFilter bool      `json:"ignoreEndpointFilter,omitempty" doc:"Also replay events the endpoint's account/folder filter would skip" example:"false"`
}

// EventsReplayResult is the job result payload.
type EventsReplayResult struct {
	Replayed int `json:"replayed"`
	Skipped  int `json:"skipped"`
	Errors   int `json:"errors"`
}

// ReplayEvents queues an events.replay job that walks stored events onto one endpoint.
func (s *Service) ReplayEvents(ctx context.Context, req EventsReplayRequest) (*jobs.Job, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	if !store.IsID(store.PrefixEndpoint, req.EndpointID) {
		return nil, errs.New(errs.CodeValidationFailed, "endpointId is required and must be a webhook endpoint id.")
	}
	if req.From.IsZero() {
		return nil, errs.New(errs.CodeValidationFailed, "from is required.")
	}
	now := s.now()
	if req.To.IsZero() {
		req.To = now
	}
	if req.From.After(req.To) {
		return nil, errs.New(errs.CodeValidationFailed, "from must not be after to.")
	}
	cutoff := now.Add(-s.eventsRetention())
	if req.From.Before(cutoff) {
		return nil, errs.New(errs.CodeReplayWindowExceeded, "from is outside the event retention window.")
	}
	if s.Events == nil {
		return nil, errs.New(errs.CodeInternalError, "Event store is not configured.")
	}
	if len(req.Types) > 0 {
		if _, err := s.Events.CatchUp(ctx, now, "", req.Types, "", 1); err != nil {
			return nil, err // invalid type filter grammar
		}
	}
	if _, err := s.GetEndpoint(ctx, req.EndpointID); err != nil {
		return nil, err
	}
	if s.Jobs == nil {
		return nil, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	job, err := s.Jobs.Create(ctx, jobKindEventsReplay, req)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "events.replay", req.EndpointID, audit.Changes{
		"from": {After: req.From}, "to": {After: req.To}, "types": {After: req.Types},
		"ignoreEndpointFilter": {After: req.IgnoreEndpointFilter}, "jobId": {After: job.ID},
	})
	return job, nil
}

func (s *Service) eventsRetention() time.Duration {
	if s.Events != nil && s.Events.Retention != nil {
		if d := s.Events.Retention(); d > 0 {
			return d
		}
	}
	return 7 * 24 * time.Hour
}

func (s *Service) handleEventsReplay(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var req EventsReplayRequest
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &req); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid events.replay payload.")
		}
	}
	org := j.OrganizationID
	if org == "" {
		return nil, errs.New(errs.CodeValidationFailed, "events.replay requires an organisation.")
	}
	ctx = store.WithOrganization(ctx, org)
	if req.To.IsZero() {
		req.To = s.now()
	}

	endpoint, err := s.Repo.endpointForWorker(ctx, org, req.EndpointID)
	if err != nil {
		return nil, err
	}

	catchTypes, catchAccount := replayCatchUpFilters(endpoint, req)
	result := EventsReplayResult{}

	// Pass 1: count matching events for progress total.
	total, err := s.countEventsReplay(ctx, req, endpoint, catchTypes, catchAccount)
	if err != nil {
		return nil, err
	}
	r.Progress(ctx, 0, total)
	if total == 0 {
		return result, nil
	}

	interval := time.Second / eventsReplayRatePerSec
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var paced bool
	var done int64
	afterID := ""

	for {
		if r.Cancelled() {
			return result, errs.New(errs.CodeConflict, "Event replay cancelled.")
		}
		batch, err := s.Events.CatchUp(ctx, req.From, afterID, catchTypes, catchAccount, eventsReplayBatchSize)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		stop := false
		for i := range batch {
			ev := batch[i]
			if ev.Time.After(req.To) {
				stop = true
				break
			}
			if r.Cancelled() {
				return result, errs.New(errs.CodeConflict, "Event replay cancelled.")
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if !eventPassesReplayFilter(endpoint, ev, req) {
				result.Skipped++
				continue
			}
			if paced {
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-ticker.C:
				}
			}
			paced = true
			if err := s.enqueueEventReplay(ctx, ev, endpoint); err != nil {
				result.Errors++
			} else {
				result.Replayed++
			}
			done++
			r.Progress(ctx, done, total)
		}
		afterID = batch[len(batch)-1].ID
		if stop || len(batch) < eventsReplayBatchSize {
			break
		}
	}
	return result, nil
}

func (s *Service) countEventsReplay(
	ctx context.Context,
	req EventsReplayRequest,
	endpoint *Endpoint,
	catchTypes []string,
	catchAccount string,
) (int64, error) {
	var total int64
	afterID := ""
	for {
		batch, err := s.Events.CatchUp(ctx, req.From, afterID, catchTypes, catchAccount, eventsReplayBatchSize)
		if err != nil {
			return 0, err
		}
		if len(batch) == 0 {
			return total, nil
		}
		for i := range batch {
			if batch[i].Time.After(req.To) {
				return total, nil
			}
			if eventPassesReplayFilter(endpoint, batch[i], req) {
				total++
			}
		}
		afterID = batch[len(batch)-1].ID
		if len(batch) < eventsReplayBatchSize {
			return total, nil
		}
	}
}

// enqueueEventReplay inserts a replay delivery for the event→endpoint pair and publishes it.
func (s *Service) enqueueEventReplay(ctx context.Context, event events.Event, endpoint *Endpoint) error {
	now := s.now()
	delivery := &Delivery{
		ID: storeDeliveryID(), OrganizationID: event.EBOrg, AccountID: event.EBAccount,
		EventID: event.ID, EventType: event.Type, EndpointID: endpoint.ID,
		State: StatePending, Attempts: []DeliveryAttempt{}, IsReplay: true,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(s.retention()), Version: 1,
	}
	if original, err := s.Repo.deliveryByIdentity(ctx, event.ID, endpoint.ID, ""); err == nil && original != nil && !original.IsReplay {
		delivery.ReplayOf = original.ID
	}
	inserted, err := s.Repo.insertDelivery(ctx, delivery)
	if err != nil {
		return err
	}
	if !inserted {
		return errs.New(errs.CodeConflict, "The replay already exists.")
	}
	if delivery.ReplayOf != "" {
		_ = s.Repo.markReplayed(ctx, delivery.ReplayOf, delivery.OrganizationID, now)
	}
	return s.publishDelivery(ctx, delivery)
}

func replayCatchUpFilters(endpoint *Endpoint, req EventsReplayRequest) (types []string, accountID string) {
	types = append([]string(nil), req.Types...)
	if req.IgnoreEndpointFilter {
		return types, ""
	}
	if len(types) == 0 && len(endpoint.Events) > 0 && !slices.Contains(endpoint.Events, "*") {
		types = append([]string(nil), endpoint.Events...)
	}
	if len(endpoint.Filter.AccountIDs) == 1 {
		accountID = endpoint.Filter.AccountIDs[0]
	}
	return types, accountID
}

func eventPassesReplayFilter(endpoint *Endpoint, event events.Event, req EventsReplayRequest) bool {
	if event.Type == events.EndpointTest {
		return false
	}
	if len(req.Types) > 0 && !typesMatch(req.Types, event.Type) {
		return false
	}
	if req.IgnoreEndpointFilter {
		return true
	}
	if !containsEvent(endpoint.Events, event.Type) {
		return false
	}
	if len(endpoint.Filter.AccountIDs) > 0 && !slices.Contains(endpoint.Filter.AccountIDs, event.EBAccount) {
		return false
	}
	if len(endpoint.Filter.MailboxPaths) == 0 {
		return true
	}
	var data map[string]any
	if json.Unmarshal(event.Data, &data) != nil {
		return false
	}
	mailbox, _ := data["mailbox"].(map[string]any)
	path, _ := mailbox["path"].(string)
	return slices.Contains(endpoint.Filter.MailboxPaths, path)
}

func typesMatch(filters []string, typ string) bool {
	for _, raw := range filters {
		for _, f := range strings.Split(raw, ",") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if strings.HasSuffix(f, ".*") {
				family := strings.TrimSuffix(f, ".*")
				if family != "" && strings.HasPrefix(typ, family+".") {
					return true
				}
				continue
			}
			if f == typ {
				return true
			}
		}
	}
	return false
}
