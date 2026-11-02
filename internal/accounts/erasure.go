package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// eraseByAccount is derived from tenancy.CollectionRegistry (W34 / S11 §5.1).
var eraseByAccount = tenancy.AccountEraseTargets()

// RegisterHandlers wires job kinds used by this package.
func (s *Service) RegisterHandlers() {
	if s.Jobs == nil {
		return
	}
	s.Jobs.Handle("erasure", s.handleErasure)
	s.Jobs.Handle("account.resync", func(context.Context, *jobs.Job, jobs.Reporter) (any, error) {
		return map[string]string{"status": "queued"}, nil
	})
	s.Jobs.Handle("account.reconnect", func(context.Context, *jobs.Job, jobs.Reporter) (any, error) {
		return map[string]string{"status": "queued"}, nil
	})
}

type erasurePayload struct {
	AccountID      string `json:"accountId"`
	OrganizationID string `json:"organizationId"`
}

func (s *Service) handleErasure(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var p erasurePayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.AccountID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "erasure payload requires accountId.")
	}
	org := p.OrganizationID
	if org == "" {
		org = j.OrganizationID
	}
	ctx = store.WithOrganization(ctx, org)

	counts := map[string]int64{}
	totalSteps := int64(len(eraseByAccount) + 3) // collections + s3 + logs + account
	var done int64

	report := func() {
		r.Progress(ctx, done, totalSteps)
	}
	report()

	// Sandbox captures live under sandbox/<org>/<captureId>.eml (no account in the key): delete them by the
	// records' rawRef before the records themselves go.
	if s.Objects != nil {
		n, err := s.eraseCaptureObjects(ctx, p.AccountID)
		if err != nil {
			return nil, err
		}
		counts["s3Captures"] = n
	}

	// Collaborators registered via RegisterEraseHook (e.g. exports S3 prefixes) before collection deletes.
	if err := runEraseHooks(ctx, org, p.AccountID); err != nil {
		return nil, err
	}

	for _, t := range eraseByAccount {
		if r.Cancelled() {
			return nil, errs.New(errs.CodeConflict, "Erasure cancelled.")
		}
		n, err := s.deleteByAccountKey(ctx, t.Coll, string(t.Key), p.AccountID)
		if err != nil {
			return nil, err
		}
		counts[t.Coll] = n
		done++
		report()
	}

	if s.Objects != nil {
		n, err := s.eraseObjects(ctx, org, p.AccountID)
		if err != nil {
			return nil, err
		}
		counts["s3"] = int64(n)
	}
	done++
	report()

	if s.Logs != nil {
		n, _, err := s.Logs.Erase(ctx, p.AccountID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.logs")
		}
		counts["logs"] = n
	}
	done++
	report()

	if r.Cancelled() {
		return nil, errs.New(errs.CodeConflict, "Erasure cancelled.")
	}

	// Emit account.deleted, audit, then remove the account document (idempotent if already gone).
	var a Account
	err := s.coll().FindOne(ctx, bson.M{"_id": p.AccountID, "organizationId": org}).Decode(&a)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.load")
	}
	accountGone := err != nil
	if !accountGone {
		ext := a.ExternalID
		err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
			if err := s.emit(txCtx, events.Draft{
				Type: events.AccountDeleted, OrganizationID: org, AccountID: p.AccountID, Subject: p.AccountID,
				Data: &eventtypes.AccountDeleted{AccountRef: eventtypes.AccountRef{AccountID: p.AccountID, ExternalID: ext}},
			}); err != nil {
				return err
			}
			_, err := s.coll().DeleteOne(txCtx, bson.M{"_id": p.AccountID, "organizationId": org})
			return err
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.finish")
		}
		s.audit(ctx, "account.erased", p.AccountID, audit.Changes{
			"counts": {After: counts},
		})
	}
	done++
	report()
	counts["account"] = 1
	if accountGone {
		counts["account"] = 0
	}
	return counts, nil
}

// deleteByAccountKey removes an account's documents from coll using key ("accountId" or "_id").
// A collection that does not exist yet simply deletes nothing; any real error fails the job.
func (s *Service) deleteByAccountKey(ctx context.Context, coll, key, accountID string) (int64, error) {
	if key == "" {
		key = "accountId"
	}
	res, err := s.DB.Collection(coll).DeleteMany(ctx, bson.M{key: accountID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "accounts.erase."+coll)
	}
	return res.DeletedCount, nil
}

// deleteByAccount removes by accountId (tests / callers that still use the old name).
func (s *Service) deleteByAccount(ctx context.Context, coll, accountID string) (int64, error) {
	return s.deleteByAccountKey(ctx, coll, "accountId", accountID)
}

// eraseCaptureObjects deletes the S3 objects of the account's sandbox captures.
func (s *Service) eraseCaptureObjects(ctx context.Context, accountID string) (int64, error) {
	cur, err := s.DB.Collection("capturedMessages").Find(ctx, bson.M{"accountId": accountID, "rawRef": bson.M{"$exists": true, "$ne": ""}},
		options.Find().SetProjection(bson.M{"rawRef": 1}))
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.captures")
	}
	var rows []struct {
		RawRef string `bson:"rawRef"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.captures")
	}
	var n int64
	for _, r := range rows {
		if err := s.Objects.Delete(ctx, r.RawRef); err != nil {
			return n, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.s3")
		}
		n++
	}
	return n, nil
}

func (s *Service) eraseObjects(ctx context.Context, org, accountID string) (int, error) {
	prefixes := []string{
		fmt.Sprintf("outbound/%s/%s/", org, accountID),
		fmt.Sprintf("archive/%s/%s/", org, accountID),
		fmt.Sprintf("attachments/%s/%s/", org, accountID),
	}
	total := 0
	for _, p := range prefixes {
		n, err := s.Objects.DeletePrefix(ctx, p)
		if err != nil {
			return total, errs.Wrap(err, errs.CodeInternalError, "accounts.erase.s3")
		}
		total += n
	}
	return total, nil
}
