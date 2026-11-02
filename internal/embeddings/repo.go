package embeddings

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo reads and writes the vector store. Every method names the organisation or the account explicitly:
// the writers run outside a request, and the readers have resolved the caller's organisation already.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

// Migration creates the vector store's indexes (G1, migration 50).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "embeddings messageEmbeddings indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{
				// The query scan (organisation + account, optionally a date range) and the account purges.
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "date", Value: -1}},
				Options: options.Index().SetName("messageEmbeddings_org_account_date"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "messageId", Value: 1}},
				Options: options.Index().SetName("messageEmbeddings_message"),
			},
			mongo.IndexModel{
				// Folder purges (mailbox.deleted / reset) and the sweep's distinct accountId.
				Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "mailboxId", Value: 1}},
				Options: options.Index().SetName("messageEmbeddings_account_mailbox"),
			},
		)
	}}
}

// ReplaceMessage writes the chunks of one message (idempotent upserts by _id) and deletes chunks of an
// earlier, longer version. The filter of every upsert includes the tenant, so a row never moves tenant.
func (r *Repo) ReplaceMessage(ctx context.Context, org, acc, msgID string, chunks []Chunk) error {
	if len(chunks) > 0 {
		models := make([]mongo.WriteModel, 0, len(chunks))
		for i := range chunks {
			c := &chunks[i]
			models = append(models, mongo.NewReplaceOneModel().
				SetFilter(bson.M{"_id": c.ID, "organizationId": org, "accountId": acc}).
				SetReplacement(c).SetUpsert(true))
		}
		if _, err := r.coll().BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeConflict, "The message has vectors under another account.").WithSafe("messageId", msgID)
			}
			return errs.Wrap(err, errs.CodeInternalError, "embeddings.write")
		}
	}
	if _, err := r.coll().DeleteMany(ctx, bson.M{
		"messageId": msgID, "organizationId": org, "accountId": acc, "chunk": bson.M{"$gte": len(chunks)},
	}); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "embeddings.write")
	}
	return nil
}

// DeleteMessages deletes the chunks of the given messages of an account.
func (r *Repo) DeleteMessages(ctx context.Context, org, acc string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return r.deleteMany(ctx, bson.M{"messageId": bson.M{"$in": ids}, "organizationId": org, "accountId": acc})
}

// DeleteMailbox deletes the chunks of one folder.
func (r *Repo) DeleteMailbox(ctx context.Context, org, acc, mailboxID string) (int64, error) {
	return r.deleteMany(ctx, bson.M{"accountId": acc, "mailboxId": mailboxID, "organizationId": org})
}

// DeleteAccount deletes every chunk of an account.
func (r *Repo) DeleteAccount(ctx context.Context, acc string) (int64, error) {
	return r.deleteMany(ctx, bson.M{"accountId": acc})
}

// DeleteStale deletes the chunks of an account (or of the whole organisation when acc is empty) that were
// not computed with model ("" = every chunk).
func (r *Repo) DeleteStale(ctx context.Context, org, acc, model string) (int64, error) {
	f := bson.M{"organizationId": org}
	if acc != "" {
		f["accountId"] = acc
	}
	if model != "" {
		f["model"] = bson.M{"$ne": model}
	}
	return r.deleteMany(ctx, f)
}

func (r *Repo) deleteMany(ctx context.Context, f bson.M) (int64, error) {
	res, err := r.coll().DeleteMany(ctx, f)
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.delete")
	}
	return res.DeletedCount, nil
}

// Hashes returns the textHash of the given messages that have vectors of model (read from chunk 0).
func (r *Repo) Hashes(ctx context.Context, org, acc, model string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	chunkIDs := make([]string, len(ids))
	for i, id := range ids {
		chunkIDs[i] = chunkID(id, 0)
	}
	cur, err := r.coll().Find(ctx, bson.M{"_id": bson.M{"$in": chunkIDs}, "organizationId": org, "accountId": acc, "model": model},
		options.Find().SetProjection(bson.M{"messageId": 1, "textHash": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hashes")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var d struct {
			MessageID string `bson:"messageId"`
			TextHash  string `bson:"textHash"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hashes")
		}
		out[d.MessageID] = d.TextHash
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hashes")
	}
	return out, nil
}

// Count counts chunks matching f, at most limit (0 = no limit).
func (r *Repo) Count(ctx context.Context, f bson.M, limit int64) (int64, error) {
	opts := options.Count()
	if limit > 0 {
		opts.SetLimit(limit)
	}
	n, err := r.coll().CountDocuments(ctx, f, opts)
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.count")
	}
	return n, nil
}

// Texts returns the text of the given chunks.
func (r *Repo) Texts(ctx context.Context, org, acc string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.coll().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": org, "accountId": acc},
		options.Find().SetProjection(bson.M{"text": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.texts")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var d struct {
			ID   string `bson:"_id"`
			Text string `bson:"text"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.texts")
		}
		out[d.ID] = d.Text
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.texts")
	}
	return out, nil
}

// AccountIDs lists the accounts that have chunks (the sweep; served by the accountId-prefixed index).
func (r *Repo) AccountIDs(ctx context.Context) ([]string, error) {
	res := r.coll().Distinct(ctx, "accountId", bson.M{})
	if err := res.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.accounts")
	}
	var ids []string
	if err := res.Decode(&ids); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.accounts")
	}
	return ids, nil
}

// OrganizationOf returns the organisation of an account's chunks ("" when it has none).
func (r *Repo) OrganizationOf(ctx context.Context, acc string) (string, error) {
	var d struct {
		OrganizationID string `bson:"organizationId"`
	}
	err := r.coll().FindOne(ctx, bson.M{"accountId": acc}, options.FindOne().SetProjection(bson.M{"organizationId": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "embeddings.org_of")
	}
	return d.OrganizationID, nil
}
