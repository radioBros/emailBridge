package fulltext

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	reindexBatch = 200
	// reindexTries is how often the job tries one message on a retryable error before counting it failed.
	reindexTries = 3
)

// reindexPayload is the job payload.
type reindexPayload struct {
	AccountID string `json:"accountId"`
	Limit     int    `json:"limit"`
}

// StartReindex queues a reindex job for an account (POST /v1/accounts/{accountId}/fulltext/reindex). When a
// reindex of the account is already queued or running, that job is returned instead of a second one.
func (s *Service) StartReindex(ctx context.Context, accountID string, limit int) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, accountID); err != nil {
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
	var acc accountDoc
	err := s.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": accountID, "organizationId": org},
		options.FindOne().SetProjection(bson.M{"state": 1})).Decode(&acc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.account")
	}
	if acc.State == accounts.StateDeleting {
		return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	}
	on, err := s.Switches.AccountEnabledFresh(ctx, org, accountID)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.switch")
	}
	if !on {
		return nil, errs.New(errs.CodeFeatureDisabled, "Full-text search is switched off for this account.").
			WithSafe("switch", SwitchName)
	}
	if j, err := s.activeReindex(ctx, org, accountID); err != nil || j != nil {
		return j, err
	}
	j, err := s.Jobs.Create(ctx, JobKind, reindexPayload{AccountID: accountID, Limit: limit})
	if err != nil {
		return nil, err
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "account.fulltext.reindex", audit.Target{Type: "account", ID: accountID},
			audit.Changes{"limit": audit.FieldChange{After: limit}})
	}
	return j, nil
}

// activeReindex returns a queued or running reindex job of the account, or nil.
func (s *Service) activeReindex(ctx context.Context, org, accountID string) (*jobs.Job, error) {
	var j jobs.Job
	err := s.DB.Collection(jobs.Collection).FindOne(ctx, bson.M{
		"organizationId": org, "kind": JobKind, "state": bson.M{"$in": []string{jobs.Queued, jobs.Running}},
		"payload": bson.M{"$regex": regexp.QuoteMeta(`"accountId":"` + accountID + `"`)},
	}).Decode(&j)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.active")
	}
	return s.Jobs.Get(ctx, j.ID)
}

// handleReindex is the JobKind handler: it walks the account's message index newest first (by message ID,
// which follows indexing order), up to the payload limit, and indexes the messages that have no row yet.
// It is idempotent (existing rows are skipped), reports progress after every batch and stops when cancelled.
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
	on, err := s.Switches.AccountEnabledFresh(ctx, org, p.AccountID)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.switch")
	}
	if !on {
		return nil, errs.New(errs.CodeFeatureDisabled, "Full-text search is switched off for this account.")
	}
	filter := bson.M{"accountId": p.AccountID, "organizationId": org}
	total, err := s.DB.Collection(index.Messages).CountDocuments(ctx, filter, options.Count().SetLimit(int64(p.Limit)))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.count")
	}
	r.Progress(ctx, 0, total)
	cur, err := s.DB.Collection(index.Messages).Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(int64(p.Limit)).
		SetProjection(bson.M{"_id": 1}).SetBatchSize(reindexBatch))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.walk")
	}
	defer func() { _ = cur.Close(ctx) }()
	batch := make([]string, 0, reindexBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		have, err := s.Repo.Existing(ctx, org, p.AccountID, batch)
		if err != nil {
			return err
		}
		for _, id := range batch {
			if r.Cancelled() {
				return context.Canceled
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			res.Scanned++
			if have[id] {
				res.AlreadyIndexed++
				continue
			}
			ok, err := s.reindexOne(ctx, org, p.AccountID, id)
			switch {
			case err != nil:
				res.Failed++
				s.log().WarnContext(ctx, "full-text reindex could not index a message", "messageId", id,
					"code", string(errs.CodeOf(err)), "err", err)
			case ok:
				res.Indexed++
			default:
				res.Skipped++
			}
		}
		batch = batch[:0]
		r.Progress(ctx, res.Scanned, total)
		return nil
	}
	for cur.Next(ctx) {
		var d struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.walk")
		}
		batch = append(batch, d.ID)
		if len(batch) == reindexBatch {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.reindex.walk")
	}
	if err := flush(); err != nil {
		return res, err
	}
	s.log().InfoContext(ctx, "full-text reindex finished", "accountId", p.AccountID, "scanned", res.Scanned,
		"indexed", res.Indexed, "alreadyIndexed", res.AlreadyIndexed, "skipped", res.Skipped, "failed", res.Failed)
	return res, nil
}

// reindexOne indexes one message, retrying a retryable provider error a few times (a busy connection
// usually frees up within seconds).
func (s *Service) reindexOne(ctx context.Context, org, accountID, messageID string) (bool, error) {
	wait := time.Second
	for try := 1; ; try++ {
		ok, err := s.IndexMessage(ctx, org, accountID, messageID)
		if err == nil || !errs.Retryable(err) || try >= reindexTries {
			return ok, err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return false, ctx.Err()
		case <-t.C:
		}
		wait *= 2
	}
}
