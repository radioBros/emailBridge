package accounts

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// EnsureIndexes creates accounts indexes (S1 §3).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "externalId", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"externalId": bson.M{"$exists": true}}).SetName("org_externalId"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}},
			Options: options.Index().SetName("org_id"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}},
			Options: options.Index().SetName("org_state"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "email", Value: 1}},
			Options: options.Index().SetName("org_email"),
		},
	)
}

// EnsureDelegatedIndex creates the sparse delegated.account index (W54 migration 47).
func EnsureDelegatedIndex(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "delegated.account", Value: 1}},
			Options: options.Index().SetSparse(true).SetName("delegated_account"),
		},
	)
}

// DelegatedIndexMigration is W54 migration 47.
func DelegatedIndexMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "accounts delegated index", Up: EnsureDelegatedIndex}
}
