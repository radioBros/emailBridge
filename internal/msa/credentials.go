package msa

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

const collCredentials = "msaCredentials"

// Credential is one account's MSA submission password.
type Credential struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	Hash           string    `bson:"hash"`
	UpdatedAt      time.Time `bson:"updatedAt"`
	UpdatedBy      string    `bson:"updatedBy,omitempty"`
}

// Repo stores MSA credentials.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(collCredentials) }

// EnsureIndexes creates msaCredentials indexes (migration 30).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	_, err := db.Collection(collCredentials).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "organizationId", Value: 1}}},
	})
	return err
}

// Get loads a credential by account id (no tenant filter — AUTH has no org context yet).
func (r *Repo) Get(ctx context.Context, accountID string) (*Credential, error) {
	var c Credential
	err := r.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "msa.credentials.get")
	}
	return &c, nil
}

// Upsert stores a new hash for the account.
func (r *Repo) Upsert(ctx context.Context, orgID, accountID, hash, by string) error {
	now := time.Now().UTC()
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": accountID}, bson.M{
		"$set": bson.M{
			"organizationId": orgID, "hash": hash, "updatedAt": now, "updatedBy": by,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "msa.credentials.upsert")
	}
	return nil
}

// Rehash replaces oldHash with newHash (transparent argon2id upgrade on login). It is a compare-and-set:
// nothing changes when the stored hash is no longer oldHash. updatedAt/updatedBy stay as they are — the
// password itself did not change.
func (r *Repo) Rehash(ctx context.Context, accountID, oldHash, newHash string) error {
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": accountID, "hash": oldHash}, bson.M{"$set": bson.M{"hash": newHash}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "msa.credentials.rehash")
	}
	return nil
}

// Delete removes the credential.
func (r *Repo) Delete(ctx context.Context, accountID string) error {
	_, err := r.coll().DeleteOne(ctx, bson.M{"_id": accountID})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "msa.credentials.delete")
	}
	return nil
}
