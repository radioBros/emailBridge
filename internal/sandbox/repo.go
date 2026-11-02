package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
	totalListCap     = 10_000
)

// Repo is sandbox MongoDB access. API methods use store.Scoped.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) get(ctx context.Context, id string) (*Captured, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var c Captured
	err = r.coll().FindOne(ctx, filter).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The captured message does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.get")
	}
	return &c, nil
}

func (r *Repo) delete(ctx context.Context, id string) (*Captured, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var c Captured
	err = r.coll().FindOneAndDelete(ctx, filter).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The captured message does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.delete")
	}
	return &c, nil
}

func (r *Repo) list(ctx context.Context, f ListFilter) (*List, error) {
	base, err := store.Scoped(ctx, listMatch(f))
	if err != nil {
		return nil, err
	}
	field, dir, err := listSort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := listLimit(f.Limit)
	filter, opts, err := listQuery(base, field, dir, limit, f.Cursor, f.Page)
	if err != nil {
		return nil, err
	}
	cur, err := r.coll().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Captured
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.list")
	}
	out := &List{Data: []View{}}
	if len(rows) > limit {
		rows = rows[:limit]
		token := encodeListCursor(listCursor{
			ID: rows[len(rows)-1].ID, Key: listSortKey(rows[len(rows)-1], field), Field: field, Desc: dir < 0,
		})
		out.NextCursor = &token
	}
	for i := range rows {
		out.Data = append(out.Data, ViewOf(&rows[i]))
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := r.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalListCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.list")
		}
		capped := n > totalListCap
		if capped {
			n = totalListCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

func listMatch(f ListFilter) bson.M {
	filter := bson.M{}
	if f.AccountID != "" {
		filter["accountId"] = f.AccountID
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		rangeFilter := bson.M{}
		if !f.From.IsZero() {
			rangeFilter["$gte"] = f.From.UTC()
		}
		if !f.To.IsZero() {
			rangeFilter["$lt"] = f.To.UTC()
		}
		filter["capturedAt"] = rangeFilter
	}
	if q := strings.TrimSpace(f.Text); q != "" {
		filter["subject"] = bson.M{"$regex": escapeRegex(q), "$options": "i"}
	}
	return filter
}

func escapeRegex(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '+', '*', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func listLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultListLimit
	case limit > maxListLimit:
		return maxListLimit
	default:
		return limit
	}
}

func listSort(value string) (string, int, error) {
	if value == "" {
		value = "-id"
	}
	desc := strings.HasPrefix(value, "-")
	name := strings.TrimPrefix(value, "-")
	switch name {
	case "id", "_id":
		name = "_id"
	case "capturedAt":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", value)
	}
	if desc {
		return name, -1, nil
	}
	return name, 1, nil
}

func listSortKey(c Captured, field string) any {
	if field == "capturedAt" {
		return c.CapturedAt.UTC().Format(time.RFC3339Nano)
	}
	return c.ID
}

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func listQuery(base bson.M, field string, dir, limit int, cursor string, page int) (bson.M, *options.FindOptionsBuilder, error) {
	opts := options.Find().SetLimit(int64(limit + 1))
	sort := bson.D{{Key: field, Value: dir}}
	if field != "_id" {
		sort = append(sort, bson.E{Key: "_id", Value: dir})
	}
	opts.SetSort(sort)
	if page > 0 {
		opts.SetSkip(int64(page-1) * int64(limit))
		return base, opts, nil
	}
	if cursor == "" {
		return base, opts, nil
	}
	decoded, err := decodeListCursor(cursor)
	if err != nil || decoded.Field != field || decoded.Desc != (dir < 0) {
		return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
	}
	cmp := "$gt"
	if dir < 0 {
		cmp = "$lt"
	}
	if field == "_id" {
		return bson.M{"$and": []bson.M{base, {"_id": bson.M{cmp: decoded.ID}}}}, opts, nil
	}
	key := decoded.Key
	text, ok := key.(string)
	if !ok {
		return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
	}
	when, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
	}
	window := bson.M{"$or": []bson.M{
		{field: bson.M{cmp: when}},
		{field: when, "_id": bson.M{cmp: decoded.ID}},
	}}
	return bson.M{"$and": []bson.M{base, window}}, opts, nil
}

func encodeListCursor(cursor listCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeListCursor(value string) (listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, err
	}
	var cursor listCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return listCursor{}, err
	}
	if cursor.ID == "" || cursor.Field == "" {
		return listCursor{}, errors.New("sandbox: empty cursor")
	}
	return cursor, nil
}

// expired returns captures whose expireAt has passed (retention sweep).
func (r *Repo) expired(ctx context.Context, now time.Time, limit int64) ([]Captured, error) {
	cur, err := r.coll().Find(ctx, bson.M{"expireAt": bson.M{"$lte": now}},
		options.Find().SetLimit(limit).SetSort(bson.D{{Key: "expireAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Captured
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *Repo) deleteByID(ctx context.Context, id string) error {
	_, err := r.coll().DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// openRaw opens the S3 object of a capture.
func (s *Service) openRaw(ctx context.Context, c *Captured) (io.ReadCloser, error) {
	if c.RawRef == "" {
		return nil, errs.New(errs.CodeNotFound, "The captured message has no raw copy.")
	}
	body, _, err := s.Objects.Get(ctx, c.RawRef)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.raw")
	}
	return body, nil
}

// Migration creates capturedMessages indexes (S1 §4) including the TTL on expireAt.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "captured messages indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "capturedAt", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		)
	}}
}
