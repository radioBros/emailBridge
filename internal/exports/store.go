package exports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo persists exports.
type Repo struct {
	DB *store.DB
}

func (r *Repo) col() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) insert(ctx context.Context, e *Export) error {
	_, err := r.col().InsertOne(ctx, e)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.insert")
	}
	return nil
}

func (r *Repo) get(ctx context.Context, accountID, id string) (*Export, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id, "accountId": accountID})
	if err != nil {
		return nil, err
	}
	var e Export
	err = r.col().FindOne(ctx, filter).Decode(&e)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeExportNotFound, "The export does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.get")
	}
	e.decodeError()
	return &e, nil
}

func (r *Repo) getByID(ctx context.Context, orgID, id string) (*Export, error) {
	var e Export
	err := r.col().FindOne(ctx, bson.M{"_id": id, "organizationId": orgID}).Decode(&e)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeExportNotFound, "The export does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.getByID")
	}
	e.decodeError()
	return &e, nil
}

func (r *Repo) replace(ctx context.Context, e *Export) error {
	if e.Error != nil {
		b, _ := json.Marshal(e.Error)
		e.ErrorText = string(b)
	}
	_, err := r.col().ReplaceOne(ctx, bson.M{"_id": e.ID}, e)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.replace")
	}
	return nil
}

func (r *Repo) countActive(ctx context.Context, accountID string) (int64, error) {
	filter, err := store.Scoped(ctx, bson.M{
		"accountId": accountID,
		"state":     bson.M{"$in": []string{StateQueued, StateRunning}},
	})
	if err != nil {
		return 0, err
	}
	n, err := r.col().CountDocuments(ctx, filter)
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "exports.countActive")
	}
	return n, nil
}

func (r *Repo) updateProgress(ctx context.Context, id string, p Progress, state string) error {
	set := bson.M{"progress": p}
	if state != "" {
		set["state"] = state
	}
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.progress")
	}
	return nil
}

func (r *Repo) setEncryption(ctx context.Context, id string, es crypto.EncryptedString) error {
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"encryption": es}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.encryption")
	}
	return nil
}

func (r *Repo) setSucceeded(ctx context.Context, id string, parts []string, messageCount, byteSize int64, prog Progress, finished, expire time.Time) error {
	prog.PartCount = len(parts)
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"state": StateSucceeded, "parts": parts, "progress": prog,
		"messageCount": messageCount, "byteSize": byteSize,
		"finishedAt": finished, "expireAt": expire,
	}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.succeed")
	}
	return nil
}

func (r *Repo) appendPart(ctx context.Context, id string, partKey string, p Progress) error {
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set":  bson.M{"progress": p},
		"$push": bson.M{"parts": partKey},
	})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.appendPart")
	}
	return nil
}

func (r *Repo) markFailed(ctx context.Context, id string, p *errs.Problem, finished time.Time) error {
	text := ""
	if p != nil {
		b, _ := json.Marshal(p)
		text = string(b)
	}
	expire := finished.Add(defaultRetention)
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"state": StateFailed, "error": text, "finishedAt": finished, "expireAt": expire,
	}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.fail")
	}
	return nil
}

func (r *Repo) markCancelled(ctx context.Context, id string, finished time.Time) error {
	expire := finished.Add(defaultRetention)
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"state": StateCancelled, "finishedAt": finished, "expireAt": expire,
	}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "exports.cancel")
	}
	return nil
}

func (r *Repo) markExpired(ctx context.Context, id string) error {
	_, err := r.col().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"state": StateExpired}})
	return err
}

func (r *Repo) listDueRetention(ctx context.Context, now time.Time, limit int64) ([]Export, error) {
	cur, err := r.col().Find(ctx, bson.M{
		"state":    StateSucceeded,
		"expireAt": bson.M{"$lte": now},
	}, options.Find().SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []Export
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnsureIndexes creates exports indexes.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "createdAt", Value: -1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "state", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	)
}
