package ai

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// Repo persists aiConfigs.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

// Get loads the org config (nil, nil when missing).
func (r *Repo) Get(ctx context.Context, orgID string) (*Config, error) {
	var c Config
	err := r.coll().FindOne(ctx, bson.M{"_id": orgID}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Upsert writes the config document.
func (r *Repo) Upsert(ctx context.Context, c *Config) error {
	c.UpdatedAt = time.Now().UTC()
	_, err := r.coll().ReplaceOne(ctx, bson.M{"_id": c.ID}, c, options.Replace().SetUpsert(true))
	return err
}

// EnsureIndexes creates aiConfigs indexes. Migration 29.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	_, err := db.Collection(Collection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "organizationId", Value: 1}}, Options: options.Index().SetUnique(true)},
	})
	return err
}
