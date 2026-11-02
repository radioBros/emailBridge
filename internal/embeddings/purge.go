package embeddings

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/fulltext"
)

const (
	// defaultSweepEvery is the sweep interval.
	defaultSweepEvery = 15 * time.Minute
	// backfillWindow / backfillRows bound the sweep's backfill per account: recent messages only (a message
	// dropped from the full queue or lost in a restart), older ones are the reindex job's.
	backfillWindow = 48 * time.Hour
	backfillRows   = 200
)

// SweepDuty is the scheduler duty "embeddings.purge" (leader only). Every SweepEvery it
//   - deletes the vectors of accounts where semantic search is no longer available (the organisation
//     switch went off, an opt-out missed by the event path, full-text or AI switched off, the account gone)
//     and the vectors of a model that is no longer the organisation's;
//   - embeds recent text-store rows of enabled accounts that have no current vectors (backfill).
func (s *Service) SweepDuty(ctx context.Context) {
	every := s.SweepEvery
	if every <= 0 {
		every = defaultSweepEvery
	}
	first := time.NewTimer(time.Minute) // let the node settle after it became leader
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.PurgeOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().WarnContext(ctx, "embeddings purge sweep failed", "err", err)
		}
		if _, err := s.BackfillOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().WarnContext(ctx, "embeddings backfill sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PurgeOnce runs one purge sweep and returns the number of chunks deleted.
func (s *Service) PurgeOnce(ctx context.Context) (int64, error) {
	ids, err := s.Repo.AccountIDs(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, acc := range ids {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		org, err := s.Repo.OrganizationOf(ctx, acc)
		if err != nil {
			return total, err
		}
		if org == "" {
			continue
		}
		actx := systemCtx(ctx, org, acc)
		reason, model, err := s.state(actx, org, acc, true)
		if err != nil {
			return total, err
		}
		var n int64
		if reason == ReasonNone {
			n, err = s.Repo.DeleteStale(actx, org, acc, model)
		} else {
			n, err = s.PurgeAccount(actx, acc)
		}
		if err != nil {
			return total, err
		}
		total += n
	}
	if total > 0 {
		s.log().InfoContext(ctx, "embeddings purge sweep deleted chunks", "chunks", total)
	}
	return total, nil
}

// BackfillOnce embeds, for every account with full-text rows and semantic search available, the recent rows
// (messages of the last 48 h, at most 200) that have no current vectors. It returns the messages embedded.
func (s *Service) BackfillOnce(ctx context.Context) (int64, error) {
	res := s.DB.Collection(fulltext.Collection).Distinct(ctx, "accountId", bson.M{})
	if err := res.Err(); err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.backfill.accounts")
	}
	var accs []string
	if err := res.Decode(&accs); err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.backfill.accounts")
	}
	var total int64
	for _, acc := range accs {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var one fulltext.Row
		if err := s.DB.Collection(fulltext.Collection).FindOne(ctx, bson.M{"accountId": acc},
			options.FindOne().SetProjection(bson.M{"organizationId": 1})).Decode(&one); err != nil || one.OrganizationID == "" {
			continue
		}
		org := one.OrganizationID
		actx := systemCtx(ctx, org, acc)
		reason, model, err := s.state(actx, org, acc, false)
		if err != nil {
			return total, err
		}
		if reason != ReasonNone {
			continue
		}
		cur, err := s.DB.Collection(fulltext.Collection).Find(actx,
			bson.M{"organizationId": org, "accountId": acc, "date": bson.M{"$gte": s.now().Add(-backfillWindow)}},
			options.Find().SetSort(bson.D{{Key: "date", Value: -1}}).SetLimit(backfillRows).
				SetProjection(bson.M{"subject": 1, "text": 1, "mailboxId": 1, "date": 1, "messageId": 1, "organizationId": 1, "accountId": 1}))
		if err != nil {
			return total, errs.Wrap(err, errs.CodeInternalError, "embeddings.backfill.rows")
		}
		var rows []fulltext.Row
		err = cur.All(actx, &rows)
		_ = cur.Close(actx)
		if err != nil {
			return total, errs.Wrap(err, errs.CodeInternalError, "embeddings.backfill.rows")
		}
		var r ReindexResult
		if err := s.reindexBatch(actx, org, acc, model, rows, &r); err != nil {
			return total, err
		}
		total += r.Embedded
	}
	if total > 0 {
		s.log().InfoContext(ctx, "embeddings backfill embedded messages", "messages", total)
	}
	return total, nil
}
