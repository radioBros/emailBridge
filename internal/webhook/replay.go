package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	jobKindDeliveriesReplay = "deliveries.replay"
	maxBulkReplayIDs        = 1000
	bulkReplayRatePerSec    = 50
)

// bulkReplayBatchSize is the keyset page size for filter-based bulk replay (tests may lower it).
var bulkReplayBatchSize = 500

// BulkReplayFilter is POST /v1/deliveries/replay (same filters as list + optional ids).
type BulkReplayFilter struct {
	EndpointID      string    `json:"endpointId,omitempty" doc:"Only deliveries to this endpoint (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	RouteID         string    `json:"routeId,omitempty" doc:"Only deliveries of this route (rte_…)" example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	EventType       string    `json:"eventType,omitempty" doc:"Only this event type" example:"message.received"`
	AccountID       string    `json:"accountId,omitempty" doc:"Only this account (acc_…)" example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	State           string    `json:"state,omitempty" doc:"Only these final states (succeeded, failed, deadLettered, cancelled), one or a comma list; default all final states" example:"deadLettered"`
	From            time.Time `json:"from,omitempty" doc:"Created at or after (RFC 3339)" example:"2026-10-26T00:00:00Z"`
	To              time.Time `json:"to,omitempty" doc:"Created at or before (RFC 3339)" example:"2026-10-27T00:00:00Z"`
	IDs             []string  `json:"ids,omitempty" doc:"Exactly these deliveries (dlv_…, at most 1000)"`
	IncludeReplayed bool      `json:"includeReplayed,omitempty" doc:"Also replay deliveries that were already replayed" example:"false"`
}

// BulkReplayResult is the job result payload.
type BulkReplayResult struct {
	Replayed int `json:"replayed"`
	Skipped  int `json:"skipped"`
	Errors   int `json:"errors"`
}

func (f BulkReplayFilter) empty() bool {
	return f.EndpointID == "" && f.RouteID == "" && f.EventType == "" && f.AccountID == "" &&
		f.State == "" && f.From.IsZero() && f.To.IsZero() && len(f.IDs) == 0
}

func (f BulkReplayFilter) toDeliveryFilter() DeliveryFilter {
	return DeliveryFilter{
		EndpointID: f.EndpointID, RouteID: f.RouteID, EventType: f.EventType,
		AccountID: f.AccountID, State: f.State, From: f.From, To: f.To,
	}
}

// BulkReplay queues a deliveries.replay job. At least one filter or ids is required.
func (s *Service) BulkReplay(ctx context.Context, filter BulkReplayFilter) (*jobs.Job, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	if filter.empty() {
		return nil, errs.New(errs.CodeValidationFailed, "At least one filter or ids is required; refusing to replay everything.")
	}
	if len(filter.IDs) > maxBulkReplayIDs {
		return nil, errs.New(errs.CodeValidationFailed, "ids must contain at most 1000 delivery IDs.")
	}
	for _, id := range filter.IDs {
		if !store.IsID(store.PrefixDelivery, id) {
			return nil, errs.New(errs.CodeValidationFailed, "ids contains an invalid delivery ID.")
		}
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && filter.From.After(filter.To) {
		return nil, errs.New(errs.CodeValidationFailed, "from must not be after to.")
	}
	if _, err := bulkReplayStates(filter.State); err != nil {
		return nil, err
	}
	if s.Jobs == nil {
		return nil, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	job, err := s.Jobs.Create(ctx, jobKindDeliveriesReplay, filter)
	if err != nil {
		return nil, err
	}
	s.auditDelivery(ctx, "delivery.bulkReplay", job.ID, audit.Changes{
		"filter": {After: filter}, "jobId": {After: job.ID},
	})
	return job, nil
}

// RegisterHandlers wires job kinds used by this package.
func (s *Service) RegisterHandlers() {
	if s.Jobs == nil {
		return
	}
	s.Jobs.Handle(jobKindDeliveriesReplay, s.handleBulkReplay)
	s.Jobs.Handle(jobKindEventsReplay, s.handleEventsReplay)
}

func (s *Service) handleBulkReplay(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var filter BulkReplayFilter
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &filter); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid deliveries.replay payload.")
		}
	}
	org := j.OrganizationID
	if org == "" {
		return nil, errs.New(errs.CodeValidationFailed, "deliveries.replay requires an organisation.")
	}
	ctx = store.WithOrganization(ctx, org)

	result := BulkReplayResult{}
	interval := time.Second / bulkReplayRatePerSec
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var paced bool

	replayOne := func(original *Delivery) {
		if paced {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		paced = true
		_, err := s.replayDelivery(ctx, original, false)
		switch {
		case err == nil:
			result.Replayed++
		case errors.Is(err, errReplaySkipped):
			result.Skipped++
		default:
			result.Errors++
		}
	}

	if len(filter.IDs) > 0 {
		rows, err := s.Repo.listBulkReplayByIDs(ctx, filter.IDs)
		if err != nil {
			return nil, err
		}
		total := int64(len(rows))
		r.Progress(ctx, 0, total)
		for i := range rows {
			if r.Cancelled() {
				return result, errs.New(errs.CodeConflict, "Bulk replay cancelled.")
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			original := &rows[i]
			switch {
			case !isFinal(original.State):
				result.Skipped++
			case !filter.IncludeReplayed && original.ReplayedAt != nil:
				result.Skipped++
			default:
				replayOne(original)
			}
			r.Progress(ctx, int64(i+1), total)
		}
		return result, nil
	}

	// Snapshot matching ids first, then replay by id. Cursor pagination while markReplayed /
	// insertDelivery mutates the collection raced with the paced loop (IncludeReplayed runs
	// especially saw short counts under load).
	ids, err := s.Repo.listBulkReplayIDs(ctx, filter)
	if err != nil {
		return nil, err
	}
	total := int64(len(ids))
	r.Progress(ctx, 0, total)
	if total == 0 {
		return result, nil
	}

	for i, id := range ids {
		if r.Cancelled() {
			return result, errs.New(errs.CodeConflict, "Bulk replay cancelled.")
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		original, err := s.Repo.delivery(ctx, id)
		if err != nil {
			if errs.CodeOf(err) == errs.CodeNotFound {
				result.Skipped++
			} else {
				result.Errors++
			}
		} else {
			switch {
			case !isFinal(original.State):
				result.Skipped++
			case !filter.IncludeReplayed && original.ReplayedAt != nil:
				result.Skipped++
			default:
				replayOne(original)
			}
		}
		r.Progress(ctx, int64(i+1), total)
	}
	return result, nil
}

var errReplaySkipped = errors.New("webhook: replay skipped")

// Replay creates a new delivery with the original event identity and a fresh attempt timestamp.
func (s *Service) Replay(ctx context.Context, id string) (*Delivery, error) {
	original, err := s.Repo.delivery(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, original.EndpointID); err != nil {
		return nil, err
	}
	replay, err := s.replayDelivery(ctx, original, true)
	if err != nil {
		if errors.Is(err, errReplaySkipped) {
			return nil, errs.New(errs.CodeReplayWindowExceeded, "The original event is outside the replay window.")
		}
		return nil, err
	}
	s.auditDelivery(ctx, "delivery.replay", replay.ID, audit.Changes{
		"replayOf": {After: original.ID}, "eventId": {After: original.EventID},
	})
	return replay, nil
}

// replayDelivery inserts and publishes a replay. When strict is false, a missing event or non-final
// original returns errReplaySkipped (bulk job path).
func (s *Service) replayDelivery(ctx context.Context, original *Delivery, strict bool) (*Delivery, error) {
	if original == nil {
		return nil, errs.New(errs.CodeNotFound, "The delivery does not exist.")
	}
	if !isFinal(original.State) {
		if !strict {
			return nil, errReplaySkipped
		}
		return nil, errs.New(errs.CodeConflict, "Only a final delivery can be replayed.")
	}
	eventCtx := store.WithOrganization(ctx, original.OrganizationID)
	if _, err := s.Events.Get(eventCtx, original.EventID); err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			if !strict {
				return nil, errReplaySkipped
			}
			return nil, errs.New(errs.CodeReplayWindowExceeded, "The original event is outside the replay window.")
		}
		return nil, err
	}
	if _, err := s.Repo.endpointForWorker(ctx, original.OrganizationID, original.EndpointID); err != nil {
		return nil, err
	}
	now := s.now()
	replay := &Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: original.OrganizationID,
		AccountID: original.AccountID, EventID: original.EventID, EventType: original.EventType,
		EndpointID: original.EndpointID, RouteID: original.RouteID,
		State: StatePending, Attempts: []DeliveryAttempt{}, ReplayOf: original.ID, IsReplay: true,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(s.retention()), Version: 1,
	}
	// The mapped body is bound (AAD) to its delivery id: decrypt the original (or read a legacy plaintext
	// row) and seal it again for the replay.
	mapped, hasMapped, err := s.openMappedBody(ctx, original)
	if err != nil {
		return nil, err
	}
	if hasMapped {
		if err := s.sealMappedBody(ctx, replay, mapped); err != nil {
			return nil, err
		}
	}
	if inserted, err := s.Repo.insertDelivery(ctx, replay); err != nil || !inserted {
		if err != nil {
			return nil, err
		}
		return nil, errs.New(errs.CodeConflict, "The replay already exists.")
	}
	if err := s.Repo.markReplayed(ctx, original.ID, original.OrganizationID, now); err != nil {
		return nil, err
	}
	original.ReplayedAt = &now
	if err := s.publishDelivery(ctx, replay); err != nil {
		return nil, err
	}
	return replay, nil
}

func bulkReplayStates(userState string) ([]string, error) {
	if userState == "" {
		return append([]string(nil), finalStates...), nil
	}
	parts := splitDeliveryStates(userState)
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if !isFinal(s) {
			return nil, errs.New(errs.CodeValidationFailed, "Bulk replay state filter may only include final states.")
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return append([]string(nil), finalStates...), nil
	}
	return out, nil
}

func (r *Repo) bulkReplayMatch(f BulkReplayFilter) (bson.M, error) {
	states, err := bulkReplayStates(f.State)
	if err != nil {
		return nil, err
	}
	match := deliveryMatch(f.toDeliveryFilter())
	if len(states) == 1 {
		match["state"] = states[0]
	} else {
		match["state"] = bson.M{"$in": states}
	}
	if !f.IncludeReplayed {
		match["replayedAt"] = bson.M{"$exists": false}
	}
	return match, nil
}

// listBulkReplayIDs returns every matching delivery id in stable (eventId, _id) order.
func (r *Repo) listBulkReplayIDs(ctx context.Context, f BulkReplayFilter) ([]string, error) {
	match, err := r.bulkReplayMatch(f)
	if err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, match)
	if err != nil {
		return nil, err
	}
	cur, err := r.DB.Collection(DeliveriesCollection).Find(ctx, base, options.Find().
		SetSort(bson.D{{Key: "eventId", Value: 1}, {Key: "_id", Value: 1}}).
		SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var ids []string
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		ids = append(ids, row.ID)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (r *Repo) listBulkReplayByIDs(ctx context.Context, ids []string) ([]Delivery, error) {
	base, err := store.Scoped(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	cur, err := r.DB.Collection(DeliveriesCollection).Find(ctx, base, options.Find().SetSort(bson.D{
		{Key: "eventId", Value: 1}, {Key: "_id", Value: 1},
	}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Delivery
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Delivery{}
	}
	return rows, nil
}

func (r *Repo) markReplayed(ctx context.Context, deliveryID, orgID string, at time.Time) error {
	_, err := r.DB.Collection(DeliveriesCollection).UpdateOne(ctx,
		bson.M{"_id": deliveryID, "organizationId": orgID},
		bson.M{"$set": bson.M{"replayedAt": at, "updatedAt": at}},
	)
	return err
}
