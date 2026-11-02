package gateways

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

// Repo is MongoDB access for smtpGateways.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) insert(ctx context.Context, g *Gateway) error {
	_, err := r.coll().InsertOne(ctx, g)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeGatewayNameTaken, "An SMTP gateway with this name already exists.")
	}
	return err
}

func (r *Repo) get(ctx context.Context, id string) (*Gateway, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var g Gateway
	if err := r.coll().FindOne(ctx, filter).Decode(&g); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The SMTP gateway does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.get")
	}
	return &g, nil
}

// GetByID loads a gateway by organisation (system / submit path, no tenancy scope).
func (r *Repo) GetByID(ctx context.Context, orgID, id string) (*Gateway, error) {
	var g Gateway
	err := r.coll().FindOne(ctx, bson.M{"_id": id, "organizationId": orgID}).Decode(&g)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The SMTP gateway does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.get")
	}
	return &g, nil
}

// getByIDAny loads by id only (public webhook path).
func (r *Repo) getByIDAny(ctx context.Context, id string) (*Gateway, error) {
	var g Gateway
	err := r.coll().FindOne(ctx, bson.M{"_id": id}).Decode(&g)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The SMTP gateway does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.get")
	}
	return &g, nil
}

func (r *Repo) replace(ctx context.Context, g *Gateway, oldVersion int64) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": g.ID, "version": oldVersion})
	if err != nil {
		return err
	}
	res, err := r.coll().ReplaceOne(ctx, filter, g)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeGatewayNameTaken, "An SMTP gateway with this name already exists.")
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "gateways.replace")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) delete(ctx context.Context, g *Gateway) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": g.ID, "version": g.Version})
	if err != nil {
		return err
	}
	res, err := r.coll().DeleteOne(ctx, filter)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "gateways.delete")
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
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

func (r *Repo) list(ctx context.Context, f ListFilter) (*List, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > 200 {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		base["$or"] = []bson.M{{"name": rx}, {"host": rx}}
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
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Gateway
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.list")
	}
	out := &List{Data: []View{}}
	if len(rows) > limit {
		rows = rows[:limit]
		token := encodeCursor(listCursor{
			ID: rows[len(rows)-1].ID, Key: sortKey(rows[len(rows)-1], field), Field: field, Desc: dir < 0,
		})
		out.NextCursor = &token
	}
	for i := range rows {
		out.Data = append(out.Data, ViewOf(&rows[i], ""))
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := r.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.list")
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

func sortKey(g Gateway, field string) any {
	switch field {
	case "name":
		return g.Name
	case "createdAt":
		return g.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return g.ID
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

// EnsureIndexes creates smtpGateways indexes (migration 26 / 42).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "kind", Value: 1}}},
	)
}

// Migration returns store migration number n for smtpGateways (+ deliveryTests on first wire).
// W36 claims number 42 (kind index). Number 26 remains valid for older wiring.
func Migration(number int) store.Migration {
	name := "smtpGateways indexes"
	if number == 26 {
		name = "smtpGateways and deliveryTests indexes"
	}
	if number >= 40 {
		name = "smtpGateways kind index (W36)"
	}
	return store.Migration{Number: number, Name: name, Up: func(ctx context.Context, db *store.DB) error {
		if err := EnsureIndexes(ctx, db); err != nil {
			return err
		}
		if number == 26 {
			return db.EnsureIndexes(ctx, "deliveryTests",
				mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "createdAt", Value: -1}}},
			)
		}
		return nil
	}}
}
