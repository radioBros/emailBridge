package fulltext

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo reads and writes the text store. Writers name the organisation explicitly (they run outside a
// request); Search scopes by the caller's organisation (store.Scoped).
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

// Migration creates the text store's indexes (G3, migration 49).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "fulltext messageText indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{
				// organizationId first: every $text query must match it by equality (the tenant guard).
				Keys: bson.D{
					{Key: "organizationId", Value: 1},
					{Key: "subject", Value: "text"}, {Key: "from", Value: "text"}, {Key: "text", Value: "text"},
				},
				Options: options.Index().SetName("messageText_text").
					SetDefaultLanguage("none").
					// No field of the row is called textLanguage: every row uses the default language.
					SetLanguageOverride("textLanguage").
					SetWeights(bson.D{{Key: "subject", Value: 10}, {Key: "from", Value: 3}, {Key: "text", Value: 1}}),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "date", Value: -1}},
				Options: options.Index().SetName("messageText_org_account_date"),
			},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "mailboxId", Value: 1}},
				Options: options.Index().SetName("messageText_account_mailbox"),
			},
		)
	}}
}

// Upsert writes a row (idempotent: the _id is the message ID).
func (r *Repo) Upsert(ctx context.Context, row *Row) error {
	row.ID = row.MessageID
	_, err := r.coll().ReplaceOne(ctx,
		bson.M{"_id": row.ID, "organizationId": row.OrganizationID, "accountId": row.AccountID},
		row, options.Replace().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		// A row with this _id exists under another account or organisation: message IDs are global, so this
		// is a programming error rather than a race; never overwrite another tenant's row.
		return errs.New(errs.CodeConflict, "The message is indexed under another account.").WithSafe("messageId", row.MessageID)
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "fulltext.upsert")
	}
	return nil
}

// DeleteMessages deletes the rows of the given messages of an account.
func (r *Repo) DeleteMessages(ctx context.Context, orgID, accountID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := r.coll().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": orgID, "accountId": accountID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "fulltext.delete")
	}
	return res.DeletedCount, nil
}

// DeleteMailbox deletes the rows of one folder.
func (r *Repo) DeleteMailbox(ctx context.Context, orgID, accountID, mailboxID string) (int64, error) {
	res, err := r.coll().DeleteMany(ctx, bson.M{"accountId": accountID, "mailboxId": mailboxID, "organizationId": orgID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "fulltext.delete_mailbox")
	}
	return res.DeletedCount, nil
}

// DeleteAccount deletes every row of an account.
func (r *Repo) DeleteAccount(ctx context.Context, accountID string) (int64, error) {
	res, err := r.coll().DeleteMany(ctx, bson.M{"accountId": accountID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "fulltext.delete_account")
	}
	return res.DeletedCount, nil
}

// Existing returns which of ids already have a row in the account.
func (r *Repo) Existing(ctx context.Context, orgID, accountID string, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.coll().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": orgID, "accountId": accountID},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.existing")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var d struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.existing")
		}
		out[d.ID] = true
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.existing")
	}
	return out, nil
}

// AccountIDs lists the accounts that have rows (the purge sweep; served by the accountId-prefixed index).
func (r *Repo) AccountIDs(ctx context.Context) ([]string, error) {
	res := r.coll().Distinct(ctx, "accountId", bson.M{})
	if err := res.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.accounts")
	}
	var ids []string
	if err := res.Decode(&ids); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.accounts")
	}
	return ids, nil
}

// OrganizationOf returns the organisation of an account's rows ("" when it has none).
func (r *Repo) OrganizationOf(ctx context.Context, accountID string) (string, error) {
	var d struct {
		OrganizationID string `bson:"organizationId"`
	}
	err := r.coll().FindOne(ctx, bson.M{"accountId": accountID}, options.FindOne().SetProjection(bson.M{"organizationId": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "fulltext.org_of")
	}
	return d.OrganizationID, nil
}

// searchFilter is a validated search.
type searchFilter struct {
	Query     query
	AccountID string
	From, To  time.Time
	Page      int
	Limit     int
}

// scoredRow is a row with its text score.
type scoredRow struct {
	Row   `bson:",inline"`
	Score float64 `bson:"score"`
}

// search runs the $text query in the caller's organisation: a page of rows by score (ties: newest first)
// and the total, capped at totalCap.
func (r *Repo) search(ctx context.Context, f searchFilter) ([]scoredRow, int64, bool, error) {
	filter := bson.M{"$text": bson.M{"$search": f.Query.Search}}
	if f.AccountID != "" {
		filter["accountId"] = f.AccountID
	}
	date := bson.M{}
	if !f.From.IsZero() {
		date["$gte"] = f.From.UTC()
	}
	if !f.To.IsZero() {
		date["$lte"] = f.To.UTC()
	}
	if len(date) > 0 {
		filter["date"] = date
	}
	filter, err := store.Scoped(ctx, filter)
	if err != nil {
		return nil, 0, false, err
	}
	score := bson.M{"$meta": "textScore"}
	opts := options.Find().
		SetProjection(bson.M{"score": score}).
		SetSort(bson.D{{Key: "score", Value: score}, {Key: "date", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64(f.Page-1) * int64(f.Limit)).
		SetLimit(int64(f.Limit))
	cur, err := r.coll().Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, false, errs.Wrap(err, errs.CodeInternalError, "fulltext.search")
	}
	defer func() { _ = cur.Close(ctx) }()
	rows := []scoredRow{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, 0, false, errs.Wrap(err, errs.CodeInternalError, "fulltext.search")
	}
	n, err := r.coll().CountDocuments(ctx, filter, options.Count().SetLimit(totalCap+1))
	if err != nil {
		return nil, 0, false, errs.Wrap(err, errs.CodeInternalError, "fulltext.search.total")
	}
	capped := n > totalCap
	if capped {
		n = totalCap
	}
	return rows, n, capped, nil
}

// liveIDs returns which of ids are still in the message index of the organisation.
func (r *Repo) liveIDs(ctx context.Context, orgID string, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.DB.Collection(index.Messages).Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": orgID},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.live")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var d struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.live")
		}
		out[d.ID] = true
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.live")
	}
	return out, nil
}

// deleteOrphans deletes rows whose message left the index (the lazy half of "rows follow the index").
func (r *Repo) deleteOrphans(ctx context.Context, orgID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := r.coll().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": orgID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "fulltext.orphans")
	}
	return res.DeletedCount, nil
}
