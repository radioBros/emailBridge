package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is MongoDB access for integrationInstances.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) insert(ctx context.Context, inst *Instance) error {
	_, err := r.coll().InsertOne(ctx, inst)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeConflict, "An integration with this name already exists.")
	}
	return err
}

func (r *Repo) get(ctx context.Context, id string) (*Instance, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var inst Instance
	if err := r.coll().FindOne(ctx, filter).Decode(&inst); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The integration instance does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.get")
	}
	return &inst, nil
}

// getByID loads an instance by id without tenant scope (public callback).
func (r *Repo) getByID(ctx context.Context, id string) (*Instance, error) {
	var inst Instance
	if err := r.coll().FindOne(ctx, bson.M{"_id": id}).Decode(&inst); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The integration instance does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.getByID")
	}
	return &inst, nil
}

// listDigestCandidates returns enabled chat instances that may run a digest (unscoped).
func (r *Repo) listDigestCandidates(ctx context.Context) ([]Instance, error) {
	filter := bson.M{
		"enabled": true,
		"type": bson.M{"$in": []string{
			TypeSlack, TypeTeams, TypeDiscord, TypeTelegram, TypeMattermost,
		}},
	}
	cur, err := r.coll().Find(ctx, filter)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.listDigest")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Instance
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.listDigest")
	}
	return rows, nil
}

func (r *Repo) saveDigestCursor(ctx context.Context, id string, at, internalAt time.Time) error {
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"lastDigestAt": at, "lastDigestInternalAt": internalAt, "updatedAt": at,
	}})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "integrations.saveDigestCursor")
	}
	return nil
}

func (r *Repo) patchActionStatus(ctx context.Context, inst *Instance) error {
	set := bson.M{
		"lastActionAt": inst.LastActionAt, "lastActionOK": inst.LastActionOK,
		"lastActionError": inst.LastActionError, "lastActionDetail": inst.LastActionDetail,
		"updatedAt": inst.UpdatedAt,
	}
	_, err := r.coll().UpdateOne(ctx, bson.M{"_id": inst.ID}, bson.M{"$set": set})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "integrations.patchActionStatus")
	}
	return nil
}

func (r *Repo) replace(ctx context.Context, inst *Instance, oldVersion int64) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": inst.ID, "version": oldVersion})
	if err != nil {
		return err
	}
	res, err := r.coll().ReplaceOne(ctx, filter, inst)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeConflict, "An integration with this name already exists.")
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "integrations.replace")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) delete(ctx context.Context, inst *Instance) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": inst.ID, "version": inst.Version})
	if err != nil {
		return err
	}
	res, err := r.coll().DeleteOne(ctx, filter)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "integrations.delete")
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) listByType(ctx context.Context, typ string) ([]Instance, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if typ != "" {
		base["type"] = typ
	}
	cur, err := r.coll().Find(ctx, base, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.listByType")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Instance
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.listByType")
	}
	return rows, nil
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

type listResult struct {
	Rows        []Instance
	NextCursor  *string
	Total       *int64
	TotalCapped *bool
}

func (r *Repo) list(ctx context.Context, f ListFilter) (*listResult, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if typ := strings.TrimSpace(f.Type); typ != "" {
		base["type"] = typ
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > 200 {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		base["$or"] = []bson.M{{"name": rx}, {"type": rx}}
	}
	field, dir, err := parseSort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	opts := options.Find().SetLimit(int64(limit + 1))
	sortDoc := bson.D{{Key: field, Value: dir}}
	if field != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: dir})
	}
	opts.SetSort(sortDoc)
	filter := base
	if f.Page > 0 {
		opts.SetSkip(int64(f.Page-1) * int64(limit))
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := cursorFilter(cur, field, dir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}
	cur, err := r.coll().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Instance
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.list")
	}
	out := &listResult{Rows: rows}
	if len(rows) > limit {
		out.Rows = rows[:limit]
		token := encodeCursor(listCursor{
			ID: out.Rows[len(out.Rows)-1].ID, Key: sortKey(out.Rows[len(out.Rows)-1], field), Field: field, Desc: dir < 0,
		})
		out.NextCursor = &token
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := r.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.list")
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-createdAt"
	}
	dir = 1
	if strings.HasPrefix(s, "-") {
		dir = -1
		s = s[1:]
	}
	switch s {
	case "name":
		return "name", dir, nil
	case "createdAt":
		return "createdAt", dir, nil
	case "id", "_id":
		return "_id", dir, nil
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", s)
	}
}

func sortKey(inst Instance, field string) any {
	switch field {
	case "name":
		return inst.Name
	case "createdAt":
		return inst.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return inst.ID
	}
}

func encodeCursor(c listCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (listCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return listCursor{}, err
	}
	var c listCursor
	return c, json.Unmarshal(b, &c)
}

func cursorFilter(c listCursor, field string, dir int) (bson.M, error) {
	if c.Field != field {
		return nil, errs.New(errs.CodeValidationFailed, "The cursor does not match the sort.")
	}
	op := "$gt"
	if dir < 0 || c.Desc {
		op = "$lt"
	}
	if field == "_id" {
		return bson.M{"_id": bson.M{op: c.ID}}, nil
	}
	return bson.M{"$or": []bson.M{
		{field: bson.M{op: c.Key}},
		{field: c.Key, "_id": bson.M{op: c.ID}},
	}}, nil
}

// EnsureIndexes creates integrationInstances indexes.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "type", Value: 1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	)
}
