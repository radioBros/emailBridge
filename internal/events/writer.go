package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Collection is the event store and transactional outbox (S1 `events`).
const Collection = "events"

// doc is the stored form. data is kept as the exact JSON text of the envelope's data (not converted to
// BSON): header names may start with "$", and a JSON→BSON→JSON round trip would change number types.
type doc struct {
	ID             string     `bson:"_id"`
	OrganizationID string     `bson:"organizationId"`
	AccountID      string     `bson:"accountId,omitempty"`
	Type           string     `bson:"type"`
	Time           time.Time  `bson:"time"`
	Subject        string     `bson:"subject"`
	Data           string     `bson:"data"`
	Schema         int        `bson:"schema"`
	TraceParent    string     `bson:"traceparent,omitempty"`
	Partition      int        `bson:"partition"`
	Published      bool       `bson:"published"`
	PublishedAt    *time.Time `bson:"publishedAt,omitempty"`
	ExpireAt       time.Time  `bson:"expireAt"`
}

func (d doc) event() Event {
	return Event{
		SpecVersion: "1.0", ID: d.ID, Type: d.Type, Source: source(d.OrganizationID, d.AccountID), Subject: d.Subject,
		Time: d.Time.UTC(), DataContentType: "application/json", DataSchema: SchemaURL(d.Type, d.Schema),
		EBOrg: d.OrganizationID, EBAccount: d.AccountID, EBSchema: d.Schema, TraceParent: d.TraceParent,
		Data: json.RawMessage(d.Data),
	}
}

func source(org, acc string) string {
	s := "/organizations/" + org
	if acc != "" {
		s += "/accounts/" + acc
	}
	return s
}

// Writer inserts events into the outbox. Call Emit with the ctx of the MongoDB transaction that makes the
// state change (store.DB.WithTx): the event exists if and only if the change committed (S4 §3).
type Writer struct {
	DB         *store.DB
	Partitions int                  // EB_EVENT_PARTITIONS
	Retention  func() time.Duration // EB_RETENTION_EVENTS (default 7 days)
}

// Emit validates and stores drafts; it returns their envelopes (IDs assigned).
func (w *Writer) Emit(ctx context.Context, drafts ...Draft) ([]Event, error) {
	if len(drafts) == 0 {
		return nil, nil
	}
	ret := 7 * 24 * time.Hour
	if w.Retention != nil && w.Retention() > 0 {
		ret = w.Retention()
	}
	docs := make([]any, 0, len(drafts))
	out := make([]Event, 0, len(drafts))
	for _, d := range drafts {
		if ct, ok := Lookup(d.Type); ok && ct.Synthetic {
			return nil, errs.New(errs.CodeInternalError, fmt.Sprintf("events: %s is synthetic and never stored", d.Type))
		}
		e, err := build(ctx, d)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		docs = append(docs, doc{
			ID: e.ID, OrganizationID: e.EBOrg, AccountID: e.EBAccount, Type: e.Type, Time: e.Time, Subject: e.Subject,
			Data: string(e.Data), Schema: e.EBSchema, TraceParent: e.TraceParent,
			Partition: bus.EventPartition(e.EBAccount, w.partitions()), ExpireAt: e.Time.Add(ret),
		})
	}
	if _, err := w.DB.Collection(Collection).InsertMany(ctx, docs); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "events.emit")
	}
	return out, nil
}

func (w *Writer) partitions() int {
	if w.Partitions > 0 {
		return w.Partitions
	}
	return 64
}

// Build returns the envelope of a draft without storing it (synthetic events such as endpoint.test).
func Build(ctx context.Context, d Draft) (Event, error) { return build(ctx, d) }

// Get loads one stored event by ID within the caller's organisation (store.Scoped).
func (w *Writer) Get(ctx context.Context, id string) (Event, error) {
	f, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return Event{}, err
	}
	var d doc
	if err := w.DB.Collection(Collection).FindOne(ctx, f).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Event{}, errs.New(errs.CodeNotFound, "The event does not exist.")
		}
		return Event{}, errs.Wrap(err, errs.CodeInternalError, "events.get")
	}
	return d.event(), nil
}

// Migration creates the events indexes (S1): the outbox scan per partition, tenant listings and the TTL.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "events indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "partition", Value: 1}, {Key: "_id", Value: 1}},
				Options: options.Index().SetName("outbox").SetPartialFilterExpression(bson.M{"published": false}),
			},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "type", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		)
	}}
}
