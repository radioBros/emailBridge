package accounts

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/store"
)

// Capacity reason of an account no sync node has room for (S4 §6 connection budget).
const (
	ReasonKindCapacity = "capacity"
	ReasonCodeCapacity = "sync_capacity"
)

// MarkUnplaced records that no sync node can take the account because every node is at
// EB_SYNC_MAX_ACCOUNTS: state disconnected with reason capacity, and account.state_changed in the same
// transaction. It only touches an account nobody runs (state new or disconnected) and is a no-op when the
// account already carries the capacity reason. A node claiming the account at the same time wins: its
// Claimed/Transition moves the state away from new/disconnected, which the version-and-state filter
// respects.
func (s *Service) MarkUnplaced(ctx context.Context, accountID string) error {
	var cur Account
	if err := s.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&cur); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		return errs.Wrap(err, errs.CodeInternalError, "accounts.unplaced")
	}
	if cur.State != StateNew && cur.State != StateDisconnected {
		return nil
	}
	if cur.StateReason != nil && cur.StateReason.Code == ReasonCodeCapacity {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	reason := &StateReason{Kind: ReasonKindCapacity, Code: ReasonCodeCapacity,
		Message: "Every sync node is at its account limit (EB_SYNC_MAX_ACCOUNTS).", At: now}
	tctx := store.WithOrganization(ctx, cur.OrganizationID)
	return s.DB.WithTx(tctx, func(txCtx context.Context) error {
		res, err := s.coll().UpdateOne(txCtx,
			bson.M{"_id": accountID, "organizationId": cur.OrganizationID, "version": cur.Version, "state": cur.State},
			bson.M{"$set": bson.M{"state": StateDisconnected, "stateReason": reason, "updatedAt": now}, "$inc": bson.M{"version": 1}})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "accounts.unplaced")
		}
		if res.MatchedCount == 0 {
			return nil // changed meanwhile (claimed, patched): nothing to record
		}
		return s.emit(txCtx, events.Draft{
			Type: events.AccountStateChanged, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
			Data: &eventtypes.AccountStateChanged{
				AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: cur.ExternalID},
				From:       cur.State, To: StateDisconnected,
				Reason: &eventtypes.Reason{Kind: ReasonKindCapacity, Code: ReasonCodeCapacity, Message: reason.Message},
			},
		})
	})
}

// Unplaced implements the sync engine's optional capacity hook (internal/sync CapacityMarker).
func (ss *SyncSource) Unplaced(ctx context.Context, accountID string) error {
	return ss.Svc.MarkUnplaced(ctx, accountID)
}
