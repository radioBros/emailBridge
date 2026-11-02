package submit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is submit's MongoDB access. API methods use store.Scoped (the tenant guard); worker methods carry
// the organisation of the job, because a worker has no caller.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) insert(ctx context.Context, m *Message) error {
	if err := m.encode(); err != nil {
		return err
	}
	_, err := r.coll().InsertOne(ctx, m)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.insert")
	}
	return nil
}

// get loads a message of the caller's organisation.
func (r *Repo) get(ctx context.Context, id string) (*Message, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	return r.one(ctx, filter)
}

// forWorker loads a message without a caller; the job carries the organisation.
func (r *Repo) forWorker(ctx context.Context, orgID, id string) (*Message, error) {
	return r.one(ctx, bson.M{"_id": id, "organizationId": orgID})
}

func (r *Repo) one(ctx context.Context, filter bson.M) (*Message, error) {
	var m Message
	err := r.coll().FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The outbound message does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.get")
	}
	m.decode()
	return &m, nil
}

// claim moves a queued message to sending (S6 §3.1). It returns nil when another worker got there first
// or the message is no longer queued (cancelled, already sent).
func (r *Repo) claim(ctx context.Context, orgID, id, worker string, now time.Time) (*Message, error) {
	var m Message
	err := r.coll().FindOneAndUpdate(ctx,
		bson.M{"_id": id, "organizationId": orgID, "state": StateQueued},
		bson.M{"$set": bson.M{"state": StateSending, "worker": worker, "heartbeatAt": now}, "$inc": bson.M{"version": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.claim")
	}
	m.decode()
	return &m, nil
}

// heartbeat refreshes heartbeatAt while sending (S6 §3.7).
func (r *Repo) heartbeat(ctx context.Context, id, worker string, now time.Time) error {
	_, err := r.coll().UpdateOne(ctx,
		bson.M{"_id": id, "state": StateSending, "worker": worker},
		bson.M{"$set": bson.M{"heartbeatAt": now}})
	return err
}

// startAttempt appends the attempt the worker is about to make and returns its index.
func (r *Repo) startAttempt(ctx context.Context, m *Message, a Attempt) (int, error) {
	idx := len(m.Attempts)
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": m.ID},
		bson.M{"$push": bson.M{"attempts": a}, "$inc": bson.M{"version": 1}})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "submit.attempt")
	}
	m.Attempts = append(m.Attempts, a)
	return idx, nil
}

// setStage records the stage the attempt has reached. The client's write concern is majority (store
// .Connect), so the `data` stage is durable on a majority before DATA goes out — what makes atMostOnce
// safe across a crash (S6 §3.3).
func (r *Repo) setStage(ctx context.Context, id string, idx int, stage string) error {
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{fmt.Sprintf("attempts.%d.stage", idx): stage}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.stage")
	}
	return nil
}

// finishAttempt stores the outcome of the attempt and the new state of the message in one update. It runs
// inside the transaction that also emits the event (S6 §3.5, §3.6).
func (r *Repo) finishAttempt(ctx context.Context, id string, idx int, a Attempt, set bson.M, unset ...string) error {
	fields := bson.M{}
	for k, v := range set {
		fields[k] = v
	}
	prefix := fmt.Sprintf("attempts.%d.", idx)
	fields[prefix+"stage"] = a.Stage
	fields[prefix+"kind"] = a.Kind
	fields[prefix+"latencyMs"] = a.LatencyMs
	if a.Code > 0 {
		fields[prefix+"code"] = a.Code
	}
	if a.Enhanced != "" {
		fields[prefix+"enhanced"] = a.Enhanced
	}
	if a.Response != "" {
		fields[prefix+"response"] = a.Response
	}
	if len(a.Rejected) > 0 {
		fields[prefix+"rejected"] = a.Rejected
	}
	if a.Rewritten {
		fields[prefix+"rewritten"] = true
	}
	update := bson.M{"$set": fields, "$inc": bson.M{"version": 1}}
	if len(unset) > 0 {
		drop := bson.M{}
		for _, f := range unset {
			drop[f] = ""
		}
		update["$unset"] = drop
	}
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id}, update)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.finish")
	}
	return nil
}

// cancel refuses every state but scheduled and queued (S2 §4.3).
func (r *Repo) cancel(ctx context.Context, m *Message) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": m.ID, "state": bson.M{"$in": CancellableStates}})
	if err != nil {
		return err
	}
	res, err := r.coll().UpdateOne(ctx, filter, CancelUpdate())
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.cancel")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeOutboundNotCancellable, "Only scheduled or queued messages can be cancelled.").
			WithSafe("state", m.State)
	}
	return nil
}

// setCopyState records the result of the copy to Sent; it never touches the message state (S6 §5).
func (r *Repo) setCopyState(ctx context.Context, id, state string, attempts int, code string) error {
	set := bson.M{"copyToSent.state": state, "copyToSent.attempts": attempts}
	if code != "" {
		set["copyToSent.code"] = code
	}
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set, "$inc": bson.M{"version": 1}})
	return err
}

// due returns messages whose time has come: scheduled messages at their sendAt and queued messages at
// their nextAttemptAt (retries, and anything the API failed to publish; S6 §2.7, §6.2).
func (r *Repo) due(ctx context.Context, now time.Time, limit int) ([]Message, error) {
	cur, err := r.coll().Find(ctx, bson.M{
		"state":         bson.M{"$in": []string{StateScheduled, StateQueued}},
		"nextAttemptAt": bson.M{"$lte": now},
	}, options.Find().SetSort(bson.D{{Key: "nextAttemptAt", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	return collect(ctx, cur)
}

// queue moves a scheduled message to queued once its sendAt is reached (S6 §1).
func (r *Repo) queue(ctx context.Context, id string) error {
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id, "state": StateScheduled},
		bson.M{"$set": bson.M{"state": StateQueued}, "$inc": bson.M{"version": 1}})
	return err
}

// stale returns messages left in sending by a worker whose heartbeat stopped (S6 §3.7).
func (r *Repo) stale(ctx context.Context, before time.Time, limit int) ([]Message, error) {
	cur, err := r.coll().Find(ctx, bson.M{
		"state":       StateSending,
		"heartbeatAt": bson.M{"$lte": before},
	}, options.Find().SetSort(bson.D{{Key: "heartbeatAt", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	return collect(ctx, cur)
}

func collect(ctx context.Context, cur *mongo.Cursor) ([]Message, error) {
	defer func() { _ = cur.Close(ctx) }()
	var rows []Message
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].decode()
	}
	return rows, nil
}

// sendsSince counts the messages an organisation or account submitted in a window (quotas, S9 §2.12).
func (r *Repo) sendsSince(ctx context.Context, filter bson.M, since time.Time, limit int64) (int64, error) {
	f := bson.M{"createdAt": bson.M{"$gte": since}}
	for k, v := range filter {
		f[k] = v
	}
	return r.coll().CountDocuments(ctx, f, options.Count().SetLimit(limit))
}

// oldestSendSince returns the creation time of the oldest message matching filter in the window (quota
// Retry-After); ok is false when there is none.
func (r *Repo) oldestSendSince(ctx context.Context, filter bson.M, since time.Time) (time.Time, bool, error) {
	f := bson.M{"createdAt": bson.M{"$gte": since}}
	for k, v := range filter {
		f[k] = v
	}
	var doc struct {
		CreatedAt time.Time `bson:"createdAt"`
	}
	err := r.coll().FindOne(ctx, f, options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: 1}}).
		SetProjection(bson.M{"createdAt": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return doc.CreatedAt, true, nil
}

// ---- idempotency (S1 `idempotencyKeys`, S2 §2) ----

// idempotencyRecord stores the response of a completed POST so the same key replays it.
type idempotencyRecord struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	AccountID      string    `bson:"accountId,omitempty"`
	Method         string    `bson:"method"`
	Route          string    `bson:"route"`
	RequestHash    string    `bson:"requestHash"`
	Status         int       `bson:"status"`
	Response       string    `bson:"response"`
	CreatedAt      time.Time `bson:"createdAt"`
	ExpireAt       time.Time `bson:"expireAt"`
}

// idempotencyID is the SHA-256 of organisation, account, method, route and key (S1 §4).
func idempotencyID(orgID, accountID, method, route, key string) string {
	sum := sha256.Sum256([]byte(orgID + "|" + accountID + "|" + method + "|" + route + "|" + key))
	return hex.EncodeToString(sum[:])
}

func requestHash(body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "submit.idempotency")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r *Repo) idempotency(ctx context.Context, id string) (*idempotencyRecord, error) {
	var rec idempotencyRecord
	err := r.DB.Collection(IdempotencyCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&rec)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.idempotency")
	}
	return &rec, nil
}

func (r *Repo) putIdempotency(ctx context.Context, rec *idempotencyRecord) error {
	_, err := r.DB.Collection(IdempotencyCollection).InsertOne(ctx, rec)
	return err
}

// encode / decode keep the final error as the exact JSON of its Problem, like internal/jobs: a BSON round
// trip would rename its fields.
func (m *Message) encode() error {
	if m.FinalError == nil {
		m.FinalErrorJSON = ""
		return nil
	}
	raw, err := json.Marshal(m.FinalError)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.encode")
	}
	m.FinalErrorJSON = string(raw)
	return nil
}

func (m *Message) decode() {
	if m.FinalErrorJSON == "" {
		return
	}
	var p errs.Problem
	if json.Unmarshal([]byte(m.FinalErrorJSON), &p) == nil {
		m.FinalError = &p
	}
}

func problemJSON(err error) string {
	raw, mErr := json.Marshal(errs.ProblemFrom(err, "", ""))
	if mErr != nil {
		return ""
	}
	return string(raw)
}

// Migration creates the outboundMessages and idempotencyKeys indexes (S1 §4).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "outbound messages indexes", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "messageIdHeader", Value: 1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "state", Value: 1}, {Key: "nextAttemptAt", Value: 1}}},
		); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, IdempotencyCollection,
			mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		)
	}}
}
