package sysmail

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// RecordsCollection holds one record per queued system mail (W72 §5): the domain record behind each
// EMAILBRIDGE_SYSMAIL entry, so the queue page can list and cancel system mail without touching the stream.
const RecordsCollection = "sysmailJobs"

// Record states (W72 §5).
const (
	RecordPending   = "pending"
	RecordSending   = "sending"
	RecordSent      = "sent"
	RecordFailed    = "failed"
	RecordDropped   = "dropped"
	RecordCancelled = "cancelled"
)

// RecordTTL is how long a record is kept (the TTL index on expireAt).
const RecordTTL = 7 * 24 * time.Hour

// CancellableStates are the record states the queue page can cancel; sending is in flight.
var CancellableStates = []string{RecordPending}

// Record is the stored sysmailJobs document. The mail data (codes, links) is never stored: it lives only in
// the stream message.
type Record struct {
	ID        string    `bson:"_id"`
	Kind      string    `bson:"kind"`
	To        string    `bson:"to"`
	Locale    string    `bson:"locale,omitempty"`
	State     string    `bson:"state"`
	Attempts  int       `bson:"attempts"`
	CreatedAt time.Time `bson:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"`
	ExpireAt  time.Time `bson:"expireAt"`
}

// Records is the sysmailJobs store. A nil *Records disables records (no database, unit tests).
type Records struct {
	DB  *store.DB
	Now func() time.Time
}

// CancelUpdate cancels a record; callers pair it with a filter on CancellableStates.
func CancelUpdate(now time.Time) bson.M {
	return bson.M{"$set": bson.M{"state": RecordCancelled, "updatedAt": now.UTC()}}
}

func (r *Records) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Records) coll() *mongo.Collection { return r.DB.Collection(RecordsCollection) }

func (r *Records) insert(ctx context.Context, j job) error {
	now := r.now()
	_, err := r.coll().InsertOne(ctx, Record{
		ID: j.ID, Kind: j.Kind, To: j.To, Locale: j.Locale, State: RecordPending,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(RecordTTL),
	})
	return err
}

// claimOutcome is what claim found.
type claimOutcome int

const (
	claimed     claimOutcome = iota // pending (or a redelivered sending) moved to sending: send it
	claimSkip                       // cancelled or already finished: ack without sending
	claimMissed                     // no record (queued before records existed): send as before
)

// claim moves a pending record to sending. A record left in sending by a worker that died is claimed again
// when JetStream redelivers its message.
func (r *Records) claim(ctx context.Context, id string) (claimOutcome, error) {
	err := r.coll().FindOneAndUpdate(ctx,
		bson.M{"_id": id, "state": bson.M{"$in": []string{RecordPending, RecordSending}}},
		bson.M{"$set": bson.M{"state": RecordSending, "updatedAt": r.now()}},
	).Err()
	if err == nil {
		return claimed, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return claimMissed, err
	}
	n, err := r.coll().CountDocuments(ctx, bson.M{"_id": id}, options.Count().SetLimit(1))
	if err != nil {
		return claimMissed, err
	}
	if n == 0 {
		return claimMissed, nil
	}
	return claimSkip, nil
}

// finish records the outcome of a claimed record. retry puts it back to pending with one more attempt.
func (r *Records) finish(ctx context.Context, id, state string, retry bool) error {
	update := bson.M{"$set": bson.M{"state": state, "updatedAt": r.now()}}
	if retry {
		update = bson.M{"$set": bson.M{"state": RecordPending, "updatedAt": r.now()}, "$inc": bson.M{"attempts": 1}}
	}
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id, "state": RecordSending}, update)
	return err
}

// markFailed records a mail that never reached the stream (publish failed).
func (r *Records) markFailed(ctx context.Context, id string) error {
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id, "state": RecordPending},
		bson.M{"$set": bson.M{"state": RecordFailed, "updatedAt": r.now()}})
	return err
}

// EnsureRecordIndexes creates the sysmailJobs indexes (W72 migration 48): queue listing / chunking by state
// and the 7-day TTL.
func EnsureRecordIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, RecordsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "state", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	)
}
