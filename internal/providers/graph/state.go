package graph

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
		return nil, errs.Wrap(err, errs.CodeInternalError, "graph.state.load")
	}
	normalizeState(&st)
	return &st, nil
}

// Save upserts state (small cursor document only — no per-message maps).
func (r *StateRepo) Save(ctx context.Context, st *SyncState) error {
	st.UpdatedAt = time.Now().UTC()
	_, err := r.DB.Collection(Collection).ReplaceOne(ctx, bson.M{"_id": st.AccountID}, st, options.Replace().SetUpsert(true))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "graph.state.save")
	}
	return nil
}

// BySubscription loads state by Graph subscription id (api-node notification path).
func (r *StateRepo) BySubscription(ctx context.Context, subscriptionID string) (*SyncState, error) {
	if r == nil || r.DB == nil || subscriptionID == "" {
		return nil, nil
	}
	var st SyncState
	err := r.DB.Collection(Collection).FindOne(ctx, bson.M{"subscriptionId": subscriptionID}).Decode(&st)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "graph.state.bySubscription")
	}
	normalizeState(&st)
	return &st, nil
}

// MemoryState holds SyncState without Mongo (tests).
type MemoryState struct {
	S *SyncState
}

// Load returns the state.
func (m *MemoryState) Load(_ context.Context, acc *Account) (*SyncState, error) {
	if m.S == nil {
		m.S = newState(acc)
	}
	return m.S, nil
}

// Save persists the state.
func (m *MemoryState) Save(_ context.Context, st *SyncState) error {
	m.S = st
	return nil
}

func newState(acc *Account) *SyncState {
	h := sha256.Sum256([]byte(acc.ID))
	uidv := binary.BigEndian.Uint32(h[:4])
	if uidv == 0 {
		uidv = 1
	}
	st := &SyncState{
		AccountID: acc.ID, OrganizationID: acc.OrganizationID, UIDValidity: uidv,
	}
	normalizeState(st)
	return st
}

func normalizeState(st *SyncState) {
	if st.DeltaLinks == nil {
		st.DeltaLinks = map[string]string{}
	}
	if st.NextUID == nil {
		st.NextUID = map[string]uint32{}
	}
	if st.MailboxIDs == nil {
		st.MailboxIDs = map[string]string{}
	}
	if st.FolderIDs == nil {
		st.FolderIDs = map[string]string{}
	}
}

// Migration creates indexes for graphSyncState (claimed number: 35). Includes subscriptionId for
// cross-node callback lookup (BySubscription).
func Migration(number int) store.Migration {
	return store.Migration{
		Number: number,
		Name:   "graph_sync_state",
		Up: func(ctx context.Context, db *store.DB) error {
			_, err := db.Collection(Collection).Indexes().CreateMany(ctx, []mongo.IndexModel{
				{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
				{Keys: bson.D{{Key: "subscriptionId", Value: 1}}, Options: options.Index().
					SetName("subscription_id").SetSparse(true)},
			})
			return err
		},
	}
}
