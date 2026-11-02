package oauth

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

// Repo is MongoDB access for oauthApps and connectLinks.
type Repo struct {
	DB *store.DB
}

func (r *Repo) apps() *mongo.Collection  { return r.DB.Collection(CollectionApps) }
func (r *Repo) links() *mongo.Collection { return r.DB.Collection(CollectionLinks) }

func (r *Repo) insertApp(ctx context.Context, a *App) error {
	_, err := r.apps().InsertOne(ctx, a)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeOauthAppNameTaken, "An OAuth app with this name already exists.")
	}
	return err
}

func (r *Repo) getApp(ctx context.Context, id string) (*App, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var a App
	if err := r.apps().FindOne(ctx, filter).Decode(&a); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.get")
	}
	return &a, nil
}

// GetAppByID loads an app by org (system path, no tenancy scope).
func (r *Repo) GetAppByID(ctx context.Context, orgID, id string) (*App, error) {
	var a App
	err := r.apps().FindOne(ctx, bson.M{"_id": id, "organizationId": orgID}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.get")
	}
	return &a, nil
}

func (r *Repo) replaceApp(ctx context.Context, a *App, oldVersion int64) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": a.ID, "version": oldVersion})
	if err != nil {
		return err
	}
	res, err := r.apps().ReplaceOne(ctx, filter, a)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeOauthAppNameTaken, "An OAuth app with this name already exists.")
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.replace")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) deleteApp(ctx context.Context, a *App) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": a.ID, "version": a.Version})
	if err != nil {
		return err
	}
	res, err := r.apps().DeleteOne(ctx, filter)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.delete")
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
	maxApps      = 50
)

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func (r *Repo) listApps(ctx context.Context, f ListFilter) (*List, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > 200 {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		base["name"] = rx
	}
	sortField, desc := "createdAt", true
	switch f.Sort {
	case "", "-createdAt":
	case "createdAt":
		desc = false
	case "name":
		sortField, desc = "name", false
	case "-name":
		sortField, desc = "name", true
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
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid cursor.")
		}
		page, err := cursorFilter(c)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, page}}
	}
	dir := 1
	if desc {
		dir = -1
	}
	opts := options.Find().SetSort(bson.D{{Key: sortField, Value: dir}, {Key: "_id", Value: dir}}).SetLimit(int64(limit + 1))
	if f.Page > 1 && f.Cursor == "" {
		opts.SetSkip(int64(f.Page-1) * int64(limit))
	}
	cur, err := r.apps().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var apps []App
	if err := cur.All(ctx, &apps); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.list")
	}
	out := &List{Data: make([]View, 0, len(apps))}
	for i, a := range apps {
		if i >= limit {
			c := listCursor{ID: a.ID, Field: sortField, Desc: desc}
			switch sortField {
			case "name":
				c.Key = a.Name
			default:
				c.Key = a.CreatedAt
			}
			s := encodeCursor(c)
			out.NextCursor = &s
			break
		}
		out.Data = append(out.Data, ViewOf(&a))
	}
	if f.IncludeTotal {
		n, err := r.apps().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.count")
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

func (r *Repo) countApps(ctx context.Context) (int64, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return 0, err
	}
	return r.apps().CountDocuments(ctx, base)
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

func cursorFilter(c listCursor) (bson.M, error) {
	op := "$gt"
	if c.Desc {
		op = "$lt"
	}
	field := c.Field
	if field == "" {
		field = "createdAt"
	}
	if field == "_id" {
		return bson.M{"_id": bson.M{op: c.ID}}, nil
	}
	return bson.M{"$or": []bson.M{
		{field: bson.M{op: c.Key}},
		{field: c.Key, "_id": bson.M{op: c.ID}},
	}}, nil
}

func (r *Repo) insertLink(ctx context.Context, l *ConnectLink) error {
	_, err := r.links().InsertOne(ctx, l)
	return err
}

func (r *Repo) getLink(ctx context.Context, id string) (*ConnectLink, error) {
	var l ConnectLink
	err := r.links().FindOne(ctx, bson.M{"_id": id}).Decode(&l)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.link")
	}
	return &l, nil
}

func (r *Repo) markLinkUsed(ctx context.Context, id string, at time.Time) error {
	res, err := r.links().UpdateOne(ctx,
		bson.M{"_id": id, "usedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"usedAt": at}},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.link.use")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	return nil
}

// markLinkUsedOnce sets usedAt on an organisation's link unless it is already set; a link that is already
// used is not an error (idempotent completion).
func (r *Repo) markLinkUsedOnce(ctx context.Context, orgID, id string, at time.Time) error {
	_, err := r.links().UpdateOne(ctx,
		bson.M{"_id": id, "organizationId": orgID, "usedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"usedAt": at}},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.link.use")
	}
	return nil
}

// EnsureIndexes creates oauthApps + connectLinks indexes.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, CollectionApps,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, CollectionLinks,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "expiresAt", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0),
		},
	)
}

// Migration returns store migration number n.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "oauthApps and connectLinks indexes", Up: EnsureIndexes}
}
