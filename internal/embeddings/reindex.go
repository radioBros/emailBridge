package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	// reindexBatch is how many text-store rows the job reads at a time.
	reindexBatch = 100
	// reindexTries is how often the job tries one request on a retryable error.
	reindexTries = 3
)

// reindexPayload is the job payload.
type reindexPayload struct {
	AccountID string `json:"accountId"`
	Limit     int    `json:"limit"`
}

// StartReindex queues a reindex job (POST /v1/accounts/{accountId}/embeddings/reindex). While one of the
// account is queued or running, that job is returned instead of a second one.
func (s *Service) StartReindex(ctx context.Context, acc string, limit int) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, acc); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	if limit == 0 {
		limit = DefaultReindexLimit
	}
	if limit < 1 || limit > MaxReindexLimit {
		return nil, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 50000.").WithSafe("max", MaxReindexLimit)
	}
	if err := s.accountUsable(ctx, org, acc); err != nil {
		return nil, err
	}
	reason, _, err := s.state(ctx, org, acc, true)
	if err != nil {
		return nil, err
	}
	if reason != ReasonNone {
		return nil, unavailable(reason)
	}
	if j, err := s.activeReindex(ctx, org, acc); err != nil || j != nil {
		return j, err
	}
	j, err := s.Jobs.Create(ctx, JobKind, reindexPayload{AccountID: acc, Limit: limit})
	if err != nil {
		return nil, err
	}
	if s.Audit != nil {
		if err := s.Audit.Record(ctx, "account.embeddings.reindex", audit.Target{Type: "account", ID: acc},
			audit.Changes{"limit": audit.FieldChange{After: limit}}); err != nil {
			s.log().WarnContext(ctx, "audit record of the embeddings reindex failed", "err", err)
		}
	}
	return j, nil
}

// activeReindex returns a queued or running reindex job of the account, or nil.
func (s *Service) activeReindex(ctx context.Context, org, acc string) (*jobs.Job, error) {
	var j jobs.Job
	err := s.DB.Collection(jobs.Collection).FindOne(ctx, bson.M{
		"organizationId": org, "kind": JobKind, "state": bson.M{"$in": []string{jobs.Queued, jobs.Running}},
		"payload": bson.M{"$regex": regexp.QuoteMeta(`"accountId":"` + acc + `"`)},
	}).Decode(&j)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.reindex.active")
	}
	return s.Jobs.Get(ctx, j.ID)
}

// handleReindex is the JobKind handler: it walks the account's text-store rows newest message first, up to
// the payload limit, and embeds the messages whose vectors are missing or out of date, several messages per
// provider request. It is idempotent, reports progress after every batch and stops when cancelled.
func (s *Service) handleReindex(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var p reindexPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || !store.IsID(store.PrefixAccount, p.AccountID) {
		return nil, errs.New(errs.CodeValidationFailed, "The reindex payload needs an accountId.")
	}
	org := j.OrganizationID
	if p.Limit <= 0 || p.Limit > MaxReindexLimit {
		p.Limit = DefaultReindexLimit
	}
	ctx = systemCtx(ctx, org, p.AccountID)
	res := ReindexResult{AccountID: p.AccountID}
	reason, model, err := s.state(ctx, org, p.AccountID, true)
	if err != nil {
		return nil, err
	}
	if reason != ReasonNone {
		return nil, unavailable(reason)
	}
	filter := bson.M{"organizationId": org, "accountId": p.AccountID}
	total, err := s.DB.Collection(fulltext.Collection).CountDocuments(ctx, filter, options.Count().SetLimit(int64(p.Limit)))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.reindex.count")
	}
	r.Progress(ctx, 0, total)
	cur, err := s.DB.Collection(fulltext.Collection).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "date", Value: -1}}).SetLimit(int64(p.Limit)). // index messageText_org_account_date: no in-memory sort
		SetProjection(bson.M{"subject": 1, "text": 1, "mailboxId": 1, "date": 1, "messageId": 1, "organizationId": 1, "accountId": 1}).
		SetBatchSize(reindexBatch))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.reindex.walk")
	}
	defer func() { _ = cur.Close(ctx) }()
	batch := make([]fulltext.Row, 0, reindexBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if r.Cancelled() {
			return context.Canceled
		}
		err := s.reindexBatch(ctx, org, p.AccountID, model, batch, &res)
		batch = batch[:0]
		r.Progress(ctx, res.Scanned, total)
		return err
	}
	for cur.Next(ctx) {
		var row fulltext.Row
		if err := cur.Decode(&row); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.reindex.walk")
		}
		batch = append(batch, row)
		if len(batch) == reindexBatch {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.reindex.walk")
	}
	if err := flush(); err != nil {
		return res, err
	}
	s.log().InfoContext(ctx, "embeddings reindex finished", "accountId", p.AccountID, "scanned", res.Scanned,
		"embedded", res.Embedded, "alreadyEmbedded", res.AlreadyEmbedded, "skipped", res.Skipped, "failed", res.Failed,
		"chunks", res.Chunks)
	return res, nil
}

// reindexBatch embeds the rows of one batch that need it, grouping messages into provider requests of at
// most embedBatch chunks. A failed request counts its messages as failed; the job goes on.
func (s *Service) reindexBatch(ctx context.Context, org, acc, model string, rows []fulltext.Row, res *ReindexResult) error {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].MessageID
	}
	have, err := s.Repo.Hashes(ctx, org, acc, model, ids)
	if err != nil {
		return err
	}
	var group []prepared
	size := 0
	send := func() error {
		if len(group) == 0 {
			return nil
		}
		n, err := s.embedWithRetry(ctx, org, acc, model, group)
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			res.Failed += int64(len(group))
			s.log().WarnContext(ctx, "embeddings reindex could not embed messages", "messages", len(group),
				"code", string(errs.CodeOf(err)), "err", err)
		default:
			res.Embedded += int64(len(group))
			res.Chunks += int64(n)
		}
		group, size = group[:0], 0
		return nil
	}
	for i := range rows {
		res.Scanned++
		p := prepare(&rows[i], model)
		if have[rows[i].MessageID] == p.hash {
			res.AlreadyEmbedded++
			continue
		}
		if len(p.chunks) == 0 {
			res.Skipped++
			continue
		}
		if size+len(p.chunks) > embedBatch {
			if err := send(); err != nil {
				return err
			}
		}
		group = append(group, p)
		size += len(p.chunks)
	}
	return send()
}

// embedWithRetry is embedGroup retrying a retryable error a few times.
func (s *Service) embedWithRetry(ctx context.Context, org, acc, model string, group []prepared) (int, error) {
	wait := time.Second
	for try := 1; ; try++ {
		n, err := s.embedGroup(ctx, org, acc, model, group)
		if err == nil || !errs.Retryable(err) || try >= reindexTries {
			return n, err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return n, ctx.Err()
		case <-t.C:
		}
		wait *= 2
	}
}
