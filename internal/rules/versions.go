package rules

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultVersionLimit = 50
	maxVersionLimit     = 200
)

// VersionListFilter is GET …/versions query.
type VersionListFilter struct {
	Limit        int
	Page         int
	IncludeTotal bool
}

// VersionListResult is GET …/versions response.
type VersionListResult struct {
	Data  []RouteVersion `json:"data" doc:"Versions, newest first."`
	Total *int64         `json:"total,omitempty" doc:"Total number of versions (only with includeTotal)." example:"3"`
}

// ListVersions returns route versions newest-first.
func (r *Repo) ListVersions(ctx context.Context, routeID string, f VersionListFilter) (*VersionListResult, error) {
	if err := tenancy.RequireRead(ctx, "routes"); err != nil {
		return nil, err
	}
	if _, err := r.Get(ctx, routeID); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{"routeId": routeID})
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultVersionLimit
	}
	if limit > maxVersionLimit {
		limit = maxVersionLimit
	}
	page := f.Page
	if page <= 0 {
		page = 1
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "version", Value: -1}}).
		SetSkip(int64(page-1) * int64(limit)).
		SetLimit(int64(limit))
	cur, err := r.DB.Collection(RouteVersionsCollection).Find(ctx, base, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []RouteVersion
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []RouteVersion{}
	}
	out := &VersionListResult{Data: rows}
	if f.IncludeTotal {
		n, err := r.DB.Collection(RouteVersionsCollection).CountDocuments(ctx, base)
		if err != nil {
			return nil, err
		}
		out.Total = &n
	}
	return out, nil
}

// RestoreVersion copies an old version onto the route as a new currentVersion (audited).
func (r *Repo) RestoreVersion(ctx context.Context, routeID string, version int, ifMatch string) (*Route, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, errs.New(errs.CodeValidationFailed, "version must be >= 1")
	}
	c, _ := tenancy.CallerFrom(ctx)
	cur, err := r.Get(ctx, routeID)
	if err != nil {
		return nil, err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	filter, err := store.Scoped(ctx, bson.M{"routeId": routeID, "version": version})
	if err != nil {
		return nil, err
	}
	var ver RouteVersion
	err = r.DB.Collection(RouteVersionsCollection).FindOne(ctx, filter).Decode(&ver)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Route version not found.")
	}
	if err != nil {
		return nil, err
	}
	if _, err := r.Engine.Compile(ver.Filter, ver.Map); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	oldDocVersion := cur.Version
	beforeFilter, beforeMap := cur.Filter, cur.Map
	cur.Filter = ver.Filter
	cur.Map = ver.Map
	cur.Builder = ver.Builder
	cur.CurrentVersion++
	cur.UpdatedAt = now
	cur.Version = oldDocVersion + 1
	verDoc := &RouteVersion{
		ID: store.NewID(store.PrefixRouteVersion), OrganizationID: cur.OrganizationID, RouteID: cur.ID,
		Version: cur.CurrentVersion, Filter: cur.Filter, Map: cur.Map, Builder: cur.Builder,
		CreatedBy: c.Actor(), CreatedAt: now,
	}
	err = r.DB.WithTx(ctx, func(sc context.Context) error {
		sf, err := store.Scoped(sc, bson.M{"_id": routeID, "version": oldDocVersion})
		if err != nil {
			return err
		}
		res, err := r.DB.Collection(RoutesCollection).ReplaceOne(sc, sf, cur)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
		}
		_, err = r.DB.Collection(RouteVersionsCollection).InsertOne(sc, verDoc)
		return err
	})
	if err != nil {
		return nil, err
	}
	r.Engine.InvalidateRoute(routeID)
	if r.Audit != nil {
		_ = r.Audit.Record(ctx, "route.restore", audit.Target{Type: "route", ID: routeID}, audit.Changes{
			"restoredFrom": {After: version},
			"filter":       {Before: beforeFilter, After: cur.Filter},
			"map":          {Before: beforeMap, After: cur.Map},
			"version":      {After: cur.CurrentVersion},
		})
	}
	return cur, nil
}
