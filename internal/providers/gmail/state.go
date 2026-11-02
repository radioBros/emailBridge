package gmail

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// StateRepo persists SyncState in MongoDB.
type StateRepo struct {
	DB *store.DB
}

// Load returns state for the account, creating a skeleton when missing.
func (r *StateRepo) Load(ctx context.Context, acc *Account) (*SyncState, error) {
	var st SyncState
	err := r.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": acc.ID}).Decode(&st)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return newState(acc), nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gmail.state.load")
	}
	if st.NextUID == nil {
		st.NextUID = map[string]uint32{}
	}
	if st.MailboxIDs == nil {
		st.MailboxIDs = map[string]string{}
	}
	return &st, nil
}

// Save upserts state.
func (r *StateRepo) Save(ctx context.Context, st *SyncState) error {
	st.UpdatedAt = time.Now().UTC()
	_, err := r.DB.Collection(Collection).ReplaceOne(ctx, bson.M{"_id": st.AccountID}, st, options.Replace().SetUpsert(true))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "gmail.state.save")
	}
	return nil
}

// FindByEmail returns accountId for a gmail provider account email (callback routing).
// LookupFn is injected by wiring (accounts package owns the collection).
type FindByEmail func(ctx context.Context, email string) (accountID, organizationID string, err error)

// Migration creates indexes for gmailSyncState.
func Migration(number int) store.Migration {
	return store.Migration{
		Number: number,
		Name:   "gmail_sync_state",
		Up: func(ctx context.Context, db *store.DB) error {
			_, err := db.Collection(Collection).Indexes().CreateMany(ctx, []mongo.IndexModel{
				{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
			})
			return err
		},
	}
}
