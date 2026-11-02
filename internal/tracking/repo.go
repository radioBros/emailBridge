package tracking

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

const (
	LinksCollection  = "trackingLinks"
	EventsCollection = "trackingEvents"
)

// LinkDoc is a stored click target.
type LinkDoc struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	OutboundID     string    `bson:"outboundId"`
	AccountID      string    `bson:"accountId,omitempty"`
	Idx            int       `bson:"idx"`
	URL            string    `bson:"url"`
	Hash           string    `bson:"hash"`
	ExpireAt       time.Time `bson:"expireAt"`
}

// EventDoc is a stored open/click/unsub hit (for retention / dedup).
type EventDoc struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	AccountID      string    `bson:"accountId,omitempty"`
	OutboundID     string    `bson:"outboundId"`
	Kind           string    `bson:"kind"`
	Recipient      string    `bson:"recipient"`
	URL            string    `bson:"url,omitempty"`
	IP             string    `bson:"ip,omitempty"`
	UserAgent      string    `bson:"userAgent,omitempty"`
	Bot            bool      `bson:"bot"`
	At             time.Time `bson:"at"`
	ExpireAt       time.Time `bson:"expireAt"`
}

// Repo persists tracking links and hit records.
type Repo struct {
	DB *store.DB
}

func (r *Repo) links() *mongo.Collection  { return r.DB.Collection(LinksCollection) }
func (r *Repo) events() *mongo.Collection { return r.DB.Collection(EventsCollection) }

func (r *Repo) insertLinks(ctx context.Context, docs []LinkDoc) error {
	if len(docs) == 0 {
		return nil
	}
	for i := range docs {
		_, err := r.links().InsertOne(ctx, docs[i])
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return errs.Wrap(err, errs.CodeInternalError, "tracking.insertLinks")
		}
	}
	return nil
}

func (r *Repo) getLink(ctx context.Context, org, outboundID string, idx int) (*LinkDoc, error) {
	var d LinkDoc
	err := r.links().FindOne(ctx, bson.M{
		"organizationId": org, "outboundId": outboundID, "idx": idx,
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The tracked link does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "tracking.getLink")
	}
	return &d, nil
}

func (r *Repo) insertEvent(ctx context.Context, e *EventDoc) error {
	_, err := r.events().InsertOne(ctx, e)
	return err
}

// outboundListID returns the List-Id token stored on an outbound message (W53).
func (r *Repo) outboundListID(ctx context.Context, org, outboundID string) string {
	var doc struct {
		ListID string `bson:"listId"`
	}
	_ = r.DB.Collection("outboundMessages").FindOne(ctx, bson.M{
		"_id": outboundID, "organizationId": org,
	}).Decode(&doc)
	return strings.Trim(strings.TrimSpace(doc.ListID), "<>")
}

// EnsureIndexes creates tracking indexes (migration 29).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, LinksCollection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "outboundId", Value: 1}, {Key: "idx", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, EventsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "outboundId", Value: 1}, {Key: "kind", Value: 1}, {Key: "at", Value: -1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	)
}

// Migration returns store migration number n for tracking indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "tracking links indexes", Up: EnsureIndexes}
}
