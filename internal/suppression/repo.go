package suppression

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is MongoDB access for suppression lists and entries.
type Repo struct {
	DB *store.DB
}

func (r *Repo) lists() *mongo.Collection   { return r.DB.Collection(ListsCollection) }
func (r *Repo) entries() *mongo.Collection { return r.DB.Collection(EntriesCollection) }

func (r *Repo) insertList(ctx context.Context, l *List) error {
	_, err := r.lists().InsertOne(ctx, l)
	if mongo.IsDuplicateKeyError(err) {
		if strings.Contains(err.Error(), "kind") {
			return errs.New(errs.CodeSuppressionListKindTaken, "A system suppression list of this kind already exists.")
		}
		return errs.New(errs.CodeSuppressionListNameTaken, "A suppression list with this name already exists.")
	}
	return err
}

func (r *Repo) getList(ctx context.Context, id string) (*List, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var l List
	if err := r.lists().FindOne(ctx, filter).Decode(&l); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The suppression list does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.getList")
	}
	return &l, nil
}

func (r *Repo) getListByKind(ctx context.Context, org, kind string) (*List, error) {
	var l List
	err := r.lists().FindOne(ctx, bson.M{"organizationId": org, "kind": kind}).Decode(&l)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.getListByKind")
	}
	return &l, nil
}

func (r *Repo) replaceList(ctx context.Context, l *List, oldVersion int64) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": l.ID, "version": oldVersion})
	if err != nil {
		return err
	}
	res, err := r.lists().ReplaceOne(ctx, filter, l)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeSuppressionListNameTaken, "A suppression list with this name already exists.")
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "suppression.replaceList")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) deleteList(ctx context.Context, l *List) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": l.ID, "version": l.Version})
	if err != nil {
		return err
	}
	res, err := r.lists().DeleteOne(ctx, filter)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "suppression.deleteList")
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	_, _ = r.entries().DeleteMany(ctx, bson.M{"listId": l.ID, "organizationId": l.OrganizationID})
	return nil
}

const (
	defaultLimit = 50
	maxLimit     = 200
	totalCap     = 10_000
)

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func (r *Repo) listLists(ctx context.Context, f ListFilter) (*ListPage, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if k := strings.TrimSpace(f.Kind); k != "" {
		base["kind"] = k
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > 200 {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		base["name"] = bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
	}
	desc := true
	switch f.Sort {
	case "", "-createdAt", "-id":
	case "createdAt", "id", "name":
		desc = false
	case "-name":
		desc = true
	default:
		return nil, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	filter := base
	dir := 1
	if desc {
		dir = -1
	}
	opts := options.Find().SetLimit(int64(limit + 1)).SetSort(bson.D{{Key: "_id", Value: dir}})
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		op := "$gt"
		if desc {
			op = "$lt"
		}
		filter = bson.M{"$and": []bson.M{base, {"_id": bson.M{op: c.ID}}}}
	} else if f.Page > 1 {
		opts.SetSkip(int64((f.Page - 1) * limit))
	}
	cur, err := r.lists().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listLists")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []List
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listLists.decode")
	}
	out := &ListPage{Data: rows}
	if len(rows) > limit {
		out.Data = rows[:limit]
		tok, _ := encodeCursor(listCursor{ID: out.Data[limit-1].ID, Field: "_id", Desc: desc})
		out.NextCursor = &tok
	}
	if f.IncludeTotal {
		n, err := r.lists().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listLists.count")
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	if out.Data == nil {
		out.Data = []List{}
	}
	return out, nil
}

func (r *Repo) upsertEntry(ctx context.Context, e *Entry) (created bool, err error) {
	filter := bson.M{"_id": e.ID}
	res, err := r.entries().UpdateOne(ctx, filter, bson.M{"$setOnInsert": e}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternalError, "suppression.upsertEntry")
	}
	return res.UpsertedCount > 0, nil
}

func (r *Repo) getEntry(ctx context.Context, listID, entryID string) (*Entry, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": entryID, "listId": listID})
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := r.entries().FindOne(ctx, filter).Decode(&e); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The suppression entry does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.getEntry")
	}
	return &e, nil
}

func (r *Repo) deleteEntry(ctx context.Context, listID, entryID string) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": entryID, "listId": listID})
	if err != nil {
		return err
	}
	res, err := r.entries().DeleteOne(ctx, filter)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "suppression.deleteEntry")
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeNotFound, "The suppression entry does not exist.")
	}
	return nil
}

func (r *Repo) listEntries(ctx context.Context, f EntryFilter) (*EntryPage, error) {
	base, err := store.Scoped(ctx, bson.M{"listId": f.ListID})
	if err != nil {
		return nil, err
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > 200 {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		base["address"] = bson.M{"$regex": regexp.QuoteMeta(strings.ToLower(q)), "$options": "i"}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	opts := options.Find().SetLimit(int64(limit + 1)).SetSort(bson.D{{Key: "_id", Value: 1}})
	filter := base
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, {"_id": bson.M{"$gt": c.ID}}}}
	} else if f.Page > 1 {
		opts.SetSkip(int64((f.Page - 1) * limit))
	}
	cur, err := r.entries().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listEntries")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Entry
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listEntries.decode")
	}
	out := &EntryPage{Data: rows}
	if len(rows) > limit {
		out.Data = rows[:limit]
		tok, _ := encodeCursor(listCursor{ID: out.Data[limit-1].ID, Field: "_id"})
		out.NextCursor = &tok
	}
	if f.IncludeTotal {
		n, err := r.entries().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.listEntries.count")
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	if out.Data == nil {
		out.Data = []Entry{}
	}
	return out, nil
}

func (r *Repo) findHits(ctx context.Context, org string, addresses []string) ([]Hit, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	cur, err := r.entries().Find(ctx, bson.M{"organizationId": org, "address": bson.M{"$in": addresses}})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.findHits")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Entry
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.findHits.decode")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	listIDs := map[string]struct{}{}
	for _, e := range rows {
		listIDs[e.ListID] = struct{}{}
	}
	ids := make([]string, 0, len(listIDs))
	for id := range listIDs {
		ids = append(ids, id)
	}
	lcur, err := r.lists().Find(ctx, bson.M{"organizationId": org, "_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.findHits.lists")
	}
	defer func() { _ = lcur.Close(ctx) }()
	kinds := map[string]string{}
	for lcur.Next(ctx) {
		var l List
		if err := lcur.Decode(&l); err != nil {
			return nil, err
		}
		kinds[l.ID] = l.Kind
	}
	hits := make([]Hit, 0, len(rows))
	for _, e := range rows {
		hits = append(hits, Hit{
			Address: e.Address, ListID: e.ListID, Reason: e.Reason, Source: e.Source,
			Kind: kinds[e.ListID], MailingListID: e.MailingListID,
		})
	}
	return hits, nil
}

func (r *Repo) allEntries(ctx context.Context, org, listID string) ([]Entry, error) {
	cur, err := r.entries().Find(ctx, bson.M{"organizationId": org, "listId": listID},
		options.Find().SetSort(bson.D{{Key: "address", Value: 1}}).SetLimit(totalCap))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.allEntries")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Entry
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "suppression.allEntries.decode")
	}
	return rows, nil
}

func encodeCursor(c listCursor) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string) (listCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return listCursor{}, errs.New(errs.CodeValidationFailed, "cursor is invalid.")
	}
	var c listCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" {
		return listCursor{}, errs.New(errs.CodeValidationFailed, "cursor is invalid.")
	}
	return c, nil
}

// EnsureIndexes creates suppression list/entry indexes (migration 28).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, ListsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{
			Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "kind", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{
				"kind": bson.M{"$in": []string{KindUnsubscribe, KindBounce, KindComplaint}},
			}),
		},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, EntriesCollection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "listId", Value: 1}, {Key: "address", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "address", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "listId", Value: 1}, {Key: "_id", Value: 1}}},
	)
}

// Migration returns store migration number n for suppression indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "suppression lists indexes", Up: EnsureIndexes}
}

// EnsureMailingListIndex replaces the unique {listId,address} index so list-scoped
// unsubscribes can coexist with a global unsub for the same address (W53 / migration 46).
func EnsureMailingListIndex(ctx context.Context, db *store.DB) error {
	col := db.Collection(EntriesCollection)
	_ = col.Indexes().DropOne(ctx, "listId_1_address_1")
	return db.EnsureIndexes(ctx, EntriesCollection,
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "listId", Value: 1},
				{Key: "address", Value: 1},
				{Key: "mailingListId", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("listId_address_mailingListId"),
		},
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "organizationId", Value: 1},
				{Key: "address", Value: 1},
				{Key: "mailingListId", Value: 1},
			},
			Options: options.Index().SetName("org_address_mailingListId"),
		},
	)
}

// MigrationMailingList returns migration 46 (list-scoped unsubscribe entries).
func MigrationMailingList(number int) store.Migration {
	return store.Migration{Number: number, Name: "suppression mailingListId index", Up: EnsureMailingListIndex}
}
