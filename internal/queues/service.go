package queues

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Publisher sends realtime messages (narrowed *realtime.Hub).
type Publisher interface {
	Publish(org, topic, typ string, data any, keys map[string]string) error
}

// Service lists and cancels queue records.
type Service struct {
	DB    *store.DB
	Jobs  *jobs.Service
	Audit *audit.Writer
	Hub   Publisher
	Log   *slog.Logger
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) coll(src *source) *mongo.Collection { return s.DB.Collection(src.collection) }

// sourceOf resolves a queue name. Events have no item list (404) and are never cancelled (409 for writes).
func sourceOf(queue string, write bool) (*source, error) {
	if src, ok := sources[queue]; ok {
		return src, nil
	}
	if queue == QueueEvents {
		if write {
			return nil, errs.New(errs.CodeQueueReadOnly, "Events are facts and are never cancelled; pause the queue on the Cluster screen instead.").
				WithSafe("queue", queue)
		}
		return nil, errs.New(errs.CodeNotFound, "The events queue has no item list; browse events with GET /v1/events.").WithSafe("queue", queue)
	}
	return nil, errs.New(errs.CodeNotFound, "Unknown queue.").WithSafe("queue", queue)
}

// authorize checks the caller for a queue and resolves its scope. write selects the cancel permission.
func authorize(ctx context.Context, src *source, all, write bool) (scope, error) {
	if all || !src.orgScoped {
		if err := tenancy.Require(ctx, tenancy.PermClusterOperate); err != nil {
			return scope{}, err
		}
		return scope{AllOrgs: true, IncludeSystem: true}, nil
	}
	var err error
	if write {
		err = tenancy.Require(ctx, src.writePerm)
	} else {
		err = tenancy.RequireRead(ctx, src.readScope)
	}
	if err != nil {
		return scope{}, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return scope{}, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	sc := scope{OrgID: org}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		sc.AccountLimited, sc.AccountIDs = true, []string{c.AccountID}
	}
	if ids, limited := tenancy.ScopedIDs(ctx, "accounts"); limited && src.itemKind == ItemOutbound {
		sc.AccountIDs = intersect(sc.AccountLimited, sc.AccountIDs, ids)
		sc.AccountLimited = true
	}
	if src.hasEndpoint {
		dIDs, dLim := tenancy.ScopedIDs(ctx, "deliveries")
		wIDs, wLim := tenancy.ScopedIDs(ctx, "webhooks")
		if dLim && wLim {
			sc.EndpointLimited, sc.EndpointIDs = true, union(dIDs, wIDs)
		}
	}
	return sc, nil
}

func intersect(limited bool, cur, ids []string) []string {
	if !limited {
		return append([]string{}, ids...)
	}
	out := []string{}
	for _, id := range cur {
		if slices.Contains(ids, id) {
			out = append(out, id)
		}
	}
	return out
}

func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, id := range b {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// scoped applies the tenant guard for an organisation scope.
func scoped(ctx context.Context, sc scope, filter bson.M) (bson.M, error) {
	if sc.AllOrgs {
		return filter, nil
	}
	return store.Scoped(store.WithOrganization(ctx, sc.OrgID), filter)
}

// validate checks a filter against a queue.
func validate(src *source, f Filter) error {
	if f.State != "" && !src.validState(f.State) {
		return errs.New(errs.CodeValidationFailed, "Unknown state for this queue.").
			WithSafe("state", f.State).WithSafe("allowed", strings.Join(src.states, ","))
	}
	if f.EndpointID != "" && !src.hasEndpoint {
		return errs.New(errs.CodeValidationFailed, "endpointId applies to webhook deliveries only.")
	}
	if f.AccountID != "" && !src.hasAccount {
		return errs.New(errs.CodeValidationFailed, "accountId does not apply to system mail.")
	}
	if f.CreatedFrom != nil && f.CreatedTo != nil && f.CreatedFrom.After(*f.CreatedTo) {
		return errs.New(errs.CodeValidationFailed, "createdFrom must not be after createdTo.")
	}
	if len(f.Q) > maxSearchLen {
		return errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
	}
	return nil
}

// ListQuery is GET /v1/queues/{queue}/items.
type ListQuery struct {
	Filter
	All   bool
	Page  int
	Limit int
	Sort  string
}

var sortFields = map[string]bson.D{
	"createdAt":      {{Key: "_id", Value: 1}},
	"-createdAt":     {{Key: "_id", Value: -1}},
	"nextAttemptAt":  {{Key: "nextAttemptAt", Value: 1}, {Key: "_id", Value: 1}},
	"-nextAttemptAt": {{Key: "nextAttemptAt", Value: -1}, {Key: "_id", Value: -1}},
}

// List returns one page of a queue's records.
func (s *Service) List(ctx context.Context, queue string, q ListQuery) (*Page, error) {
	src, err := sourceOf(queue, false)
	if err != nil {
		return nil, err
	}
	sc, err := authorize(ctx, src, q.All, false)
	if err != nil {
		return nil, err
	}
	if err := validate(src, q.Filter); err != nil {
		return nil, err
	}
	sort := q.Sort
	if sort == "" {
		sort = "createdAt"
	}
	order, ok := sortFields[sort]
	if !ok {
		return nil, errs.New(errs.CodeSortNotAllowed, "Sort by createdAt or nextAttemptAt (prefix - for descending).").WithSafe("sort", sort)
	}
	page, limit := max(q.Page, 1), q.Limit
	if limit <= 0 {
		limit = defListLimit
	}
	limit = min(limit, maxListLimit)
	filter, err := scoped(ctx, sc, andOf(src.scopeClauses(sc), src.filterClauses(q.Filter)))
	if err != nil {
		return nil, err
	}
	cur, err := s.coll(src).Find(ctx, filter, options.Find().SetSort(order).
		SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)).SetProjection(src.projection()))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.list")
	}
	var rows []row
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.list")
	}
	n, err := s.coll(src).CountDocuments(ctx, filter, options.Count().SetLimit(totalCap+1))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.list")
	}
	out := &Page{Data: make([]Item, 0, len(rows)), Total: min(n, totalCap), TotalCapped: n > totalCap, Page: page, Limit: limit}
	for _, r := range rows {
		out.Data = append(out.Data, src.item(r))
	}
	return out, nil
}

// Counts returns the record counts of a queue for the caller (organisation, or every organisation with all).
// It returns nil, nil when the caller may not read the queue, so GET /v1/queues omits the block.
func (s *Service) Counts(ctx context.Context, queue string, all bool) (*Counts, error) {
	src, ok := sources[queue]
	if !ok {
		return nil, nil
	}
	sc, err := authorize(ctx, src, all, false)
	if err != nil {
		return nil, nil //nolint:nilerr // no permission: the block is omitted, not an error
	}
	filter, err := scoped(ctx, sc, andOf(src.scopeClauses(sc), []bson.M{src.listed}))
	if err != nil {
		return nil, nil //nolint:nilerr // same: no organisation context
	}
	cur, err := s.coll(src).Aggregate(ctx, bson.A{
		bson.M{"$match": filter},
		bson.M{"$project": bson.M{"state": 1, "createdAt": 1, "a": src.attempts}},
		bson.M{"$group": bson.M{
			"_id":    bson.M{"s": "$state", "r": bson.M{"$gt": bson.A{"$a", 0}}},
			"n":      bson.M{"$sum": 1},
			"oldest": bson.M{"$min": "$createdAt"},
		}},
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.counts")
	}
	defer func() { _ = cur.Close(ctx) }()
	c := &Counts{}
	for cur.Next(ctx) {
		var g struct {
			ID struct {
				S string `bson:"s"`
				R bool   `bson:"r"`
			} `bson:"_id"`
			N      int64     `bson:"n"`
			Oldest time.Time `bson:"oldest"`
		}
		if err := cur.Decode(&g); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "queues.counts")
		}
		src.counts(c, g.ID.S, g.ID.R, g.N)
		if !src.isInFlight(g.ID.S) && !g.Oldest.IsZero() && (c.OldestAt == nil || g.Oldest.Before(*c.OldestAt)) {
			o := g.Oldest.UTC()
			c.OldestAt = &o
		}
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.counts")
	}
	return c, nil
}

// Cancel cancels records by ids (synchronously) or by filter / whole queue (as a job).
func (s *Service) Cancel(ctx context.Context, queue string, req CancelRequest) (*CancelResult, bool, error) {
	src, err := sourceOf(queue, true)
	if err != nil {
		return nil, false, err
	}
	modes := 0
	if len(req.IDs) > 0 {
		modes++
	}
	if req.Filter != nil {
		modes++
	}
	if req.All {
		modes++
	}
	if modes != 1 {
		return nil, false, errs.New(errs.CodeValidationFailed, "Send exactly one of ids, filter or all.")
	}
	all := req.Scope == ScopeAll
	if req.Scope != "" && req.Scope != ScopeAll && req.Scope != ScopeOrganization {
		return nil, false, errs.New(errs.CodeValidationFailed, "scope must be organization or all.")
	}
	sc, err := authorize(ctx, src, all, true)
	if err != nil {
		return nil, false, err
	}
	if len(req.IDs) > 0 {
		return s.cancelIDs(ctx, src, sc, req.IDs)
	}
	f := Filter{}
	if req.Filter != nil {
		if req.Filter.empty() {
			return nil, false, errs.New(errs.CodeValidationFailed, "filter needs at least one criterion; use all to cancel the whole queue.")
		}
		if err := validate(src, *req.Filter); err != nil {
			return nil, false, err
		}
		f = *req.Filter
	}
	if req.All && req.Confirm != queue {
		return nil, false, errs.New(errs.CodeQueueConfirmationRequired, "To cancel the whole queue, set confirm to the queue name.").
			WithSafe("queue", queue)
	}
	return s.startJob(ctx, src, sc, f, req.All)
}

// cancelIDs cancels each id with the same conditional update as the single cancel.
func (s *Service) cancelIDs(ctx context.Context, src *source, sc scope, ids []string) (*CancelResult, bool, error) {
	if len(ids) > MaxCancelIDs {
		return nil, false, errs.New(errs.CodeValidationFailed, "ids must contain at most 500 IDs.")
	}
	for _, id := range ids {
		if !store.IsID(src.idPrefix, id) {
			return nil, false, errs.New(errs.CodeValidationFailed, "ids contains an ID that does not belong to this queue.").WithSafe("id", id)
		}
	}
	now := s.now()
	var cancelled int64
	var skipped []Skipped
	orgs := map[string]int64{}
	uniq := slices.Compact(slices.Sorted(slices.Values(ids)))
	for _, id := range uniq {
		filter, err := scoped(ctx, sc, andOf(src.scopeClauses(sc), []bson.M{{"_id": id}, src.cancellable}))
		if err != nil {
			return nil, false, err
		}
		var doc struct {
			Org string `bson:"organizationId"`
		}
		err = s.coll(src).FindOneAndUpdate(ctx, filter, src.update(now),
			options.FindOneAndUpdate().SetProjection(bson.M{"organizationId": 1})).Decode(&doc)
		if err == nil {
			cancelled++
			orgs[doc.Org]++
			continue
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, false, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
		}
		reason, err := s.reasonOf(ctx, src, sc, id)
		if err != nil {
			return nil, false, err
		}
		skipped = append(skipped, Skipped{ID: id, Reason: reason})
	}
	if len(uniq) == 1 && len(skipped) == 1 {
		switch skipped[0].Reason {
		case ReasonNotFound:
			return nil, false, errs.New(errs.CodeNotFound, "The record does not exist in this queue.").WithSafe("id", uniq[0])
		case ReasonInFlight:
			return nil, false, errs.New(errs.CodeQueueItemNotCancellable, "The record is being transmitted right now and can no longer be cancelled.").
				WithSafe("id", uniq[0]).WithSafe("reason", ReasonInFlight)
		default:
			return nil, false, errs.New(errs.CodeQueueItemNotCancellable, "The record already reached a final state.").
				WithSafe("id", uniq[0]).WithSafe("reason", ReasonFinal)
		}
	}
	s.record(ctx, "queue.items.cancelled", src.queue, audit.Changes{
		"scope": {After: scopeName(sc)}, "requested": {After: len(uniq)}, "cancelled": {After: cancelled},
	})
	for org, n := range orgs {
		s.publish(org, Change{Queue: src.queue, Cancelled: n})
	}
	return &CancelResult{Cancelled: &cancelled, Skipped: skipped}, false, nil
}

// reasonOf explains why a record in the scope was not cancelled.
func (s *Service) reasonOf(ctx context.Context, src *source, sc scope, id string) (string, error) {
	filter, err := scoped(ctx, sc, andOf(src.scopeClauses(sc), []bson.M{{"_id": id}}))
	if err != nil {
		return "", err
	}
	var doc struct {
		State string `bson:"state"`
	}
	err = s.coll(src).FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"state": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ReasonNotFound, nil
	}
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
	}
	if src.isInFlight(doc.State) {
		return ReasonInFlight, nil
	}
	return ReasonFinal, nil
}

// cancelJob is the frozen payload of a queues.cancel job.
type cancelJob struct {
	Queue  string    `json:"queue"`
	Scope  scope     `json:"scope"`
	Filter Filter    `json:"filter"`
	All    bool      `json:"all,omitempty"`
	Before time.Time `json:"before"`
}

func (j cancelJob) match(src *source) bson.M {
	return andOf(src.scopeClauses(j.Scope), src.filterClauses(j.Filter),
		[]bson.M{src.cancellable, {"createdAt": bson.M{"$lt": j.Before}}})
}

func (s *Service) startJob(ctx context.Context, src *source, sc scope, f Filter, all bool) (*CancelResult, bool, error) {
	if s.Jobs == nil {
		return nil, false, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	p := cancelJob{Queue: src.queue, Scope: sc, Filter: f, All: all, Before: s.now()}
	filter, err := scoped(ctx, sc, p.match(src))
	if err != nil {
		return nil, false, err
	}
	matched, err := s.coll(src).CountDocuments(ctx, filter)
	if err != nil {
		return nil, false, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
	}
	if matched == 0 {
		zero := int64(0)
		return &CancelResult{Matched: &zero, Cancelled: &zero}, false, nil
	}
	j, err := s.Jobs.Create(ctx, jobKindCancel, p)
	if err != nil {
		return nil, false, err
	}
	s.record(ctx, "queue.items.cancel_requested", src.queue, audit.Changes{
		"scope": {After: scopeName(sc)}, "filter": {After: f}, "all": {After: all},
		"matched": {After: matched}, "jobId": {After: j.ID},
	})
	return &CancelResult{Job: j, Matched: &matched}, true, nil
}

// HandleCancelJob runs a queues.cancel job: keyset batches of record ids, one conditional UpdateMany per batch.
// It is idempotent: a re-run only finds records that are still cancellable.
func (s *Service) HandleCancelJob(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var p cancelJob
	if err := decodePayload(j, &p); err != nil {
		return nil, err
	}
	src, ok := sources[p.Queue]
	if !ok {
		return nil, errs.New(errs.CodeValidationFailed, "Unknown queue in queues.cancel payload.").WithSafe("queue", p.Queue)
	}
	if !p.Scope.AllOrgs && p.Scope.OrgID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "queues.cancel requires an organisation or allOrgs.")
	}
	base, err := scoped(ctx, p.Scope, p.match(src))
	if err != nil {
		return nil, err
	}
	total, err := s.coll(src).CountDocuments(ctx, base)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
	}
	res := JobResult{Matched: total}
	r.Progress(ctx, 0, total)
	last := ""
	for !r.Cancelled() && ctx.Err() == nil {
		batch, err := s.nextBatch(ctx, src, base, last)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, len(batch))
		orgs := map[string]bool{}
		for i, b := range batch {
			ids[i] = b.ID
			orgs[b.Org] = true
		}
		last = ids[len(ids)-1]
		upd, err := s.coll(src).UpdateMany(ctx, andOf([]bson.M{base, {"_id": bson.M{"$in": ids}}}), src.update(s.now()))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
		}
		res.Cancelled += upd.ModifiedCount
		res.Skipped += int64(len(ids)) - upd.ModifiedCount
		done := res.Cancelled + res.Skipped
		r.Progress(ctx, done, max(total, done))
		for org := range orgs {
			if org != j.OrganizationID {
				s.publish(org, Change{Queue: src.queue, Cancelled: upd.ModifiedCount})
			}
		}
		s.publish(j.OrganizationID, Change{Queue: src.queue, Cancelled: upd.ModifiedCount, JobID: j.ID, Done: done, Total: max(total, done)})
	}
	s.publish(j.OrganizationID, Change{Queue: src.queue, JobID: j.ID, Done: res.Cancelled + res.Skipped, Total: total, Finished: true})
	s.log().InfoContext(ctx, "queue cancel job finished", "job_id", j.ID, "queue", src.queue,
		"matched", res.Matched, "cancelled", res.Cancelled, "skipped", res.Skipped)
	return res, nil
}

type batchRow struct {
	ID  string `bson:"_id"`
	Org string `bson:"organizationId"`
}

func (s *Service) nextBatch(ctx context.Context, src *source, base bson.M, after string) ([]batchRow, error) {
	filter := base
	if after != "" {
		filter = andOf([]bson.M{base, {"_id": bson.M{"$gt": after}}})
	}
	cur, err := s.coll(src).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetLimit(cancelBatch).SetProjection(bson.M{"_id": 1, "organizationId": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
	}
	var out []batchRow
	if err := cur.All(ctx, &out); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "queues.cancel")
	}
	return out, nil
}

func scopeName(sc scope) string {
	if sc.AllOrgs {
		return ScopeAll
	}
	return ScopeOrganization
}

func (s *Service) record(ctx context.Context, action, queue string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "queue", ID: queue}, changes); err != nil {
		s.log().WarnContext(ctx, "queue audit entry not written", "err", err, "action", action)
	}
}

func (s *Service) publish(org string, c Change) {
	if s.Hub == nil || org == "" {
		return
	}
	if err := s.Hub.Publish(org, topicUpdates, typeChanged, c, map[string]string{"queue": c.Queue}); err != nil {
		s.log().Warn("queue realtime update not published", "err", err, "queue", c.Queue)
	}
}

func decodePayload(j *jobs.Job, p *cancelJob) error {
	if len(j.Payload) == 0 {
		return errs.New(errs.CodeValidationFailed, "Empty queues.cancel payload.")
	}
	if err := json.Unmarshal(j.Payload, p); err != nil {
		return errs.New(errs.CodeValidationFailed, "Invalid queues.cancel payload.")
	}
	return nil
}
