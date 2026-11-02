package crypto

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// DataKeysCollection stores wrapped DEKs (S1 §2 `dataKeys`).
const DataKeysCollection = "dataKeys"

// MongoKeyStore is the MongoDB DataKeyStore. A partial unique index guarantees one active DEK per
// organisation even when two nodes create one concurrently.
type MongoKeyStore struct{ DB *store.DB }

// Indexes returns the indexes the dataKeys collection needs (used by a migration).
func (m *MongoKeyStore) Indexes() []mongo.IndexModel {
	return []mongo.IndexModel{{
		Keys:    bson.D{{Key: "organizationId", Value: 1}},
		Options: options.Index().SetName("one_active_per_org").SetUnique(true).SetPartialFilterExpression(bson.M{"status": DEKActive}),
	}}
}

func orgFilter(orgID string) any {
	if orgID == "" {
		return nil
	}
	return orgID
}

// Active implements DataKeyStore.
func (m *MongoKeyStore) Active(ctx context.Context, orgID string) (*DataKey, error) {
	var dk DataKey
	err := m.DB.Collection(DataKeysCollection).FindOne(ctx, bson.M{"organizationId": orgFilter(orgID), "status": DEKActive}).Decode(&dk)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("crypto: active key: %w", err)
	}
	return &dk, nil
}

// Get implements DataKeyStore.
func (m *MongoKeyStore) Get(ctx context.Context, id string) (*DataKey, error) {
	var dk DataKey
	err := m.DB.Collection(DataKeysCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&dk)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("crypto: get key: %w", err)
	}
	return &dk, nil
}

// Insert implements DataKeyStore.
func (m *MongoKeyStore) Insert(ctx context.Context, k DataKey) (*DataKey, error) {
	_, err := m.DB.Collection(DataKeysCollection).InsertOne(ctx, k)
	if mongo.IsDuplicateKeyError(err) {
		org := ""
		if k.OrganizationID != nil {
			org = *k.OrganizationID
		}
		existing, gerr := m.Active(ctx, org)
		if gerr != nil || existing == nil {
			return nil, fmt.Errorf("crypto: insert key raced and no active key found: %w", err)
		}
		return existing, nil
	}
	if err != nil {
		return nil, fmt.Errorf("crypto: insert key: %w", err)
	}
	return &k, nil
}

// All implements DataKeyStore.
func (m *MongoKeyStore) All(ctx context.Context) ([]DataKey, error) {
	cur, err := m.DB.Collection(DataKeysCollection).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var out []DataKey
	return out, cur.All(ctx, &out)
}

// UpdateWrap implements DataKeyStore.
func (m *MongoKeyStore) UpdateWrap(ctx context.Context, id, kekID string, wrapped []byte) error {
	_, err := m.DB.Collection(DataKeysCollection).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"kekId": kekID, "wrapped": wrapped}})
	return err
}

// Retire implements DataKeyStore.
func (m *MongoKeyStore) Retire(ctx context.Context, id string) error {
	_, err := m.DB.Collection(DataKeysCollection).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": DEKRetired}})
	return err
}
