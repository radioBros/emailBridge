package rules

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// EndpointExists reports whether a webhook endpoint exists for the organisation.
type EndpointExists func(ctx context.Context, orgID, id string) (bool, error)

// EventLoader loads a stored event by ID for the test endpoint.
type EventLoader func(ctx context.Context, orgID, eventID string) (map[string]any, error)

// EnvLoader returns organisation env values and which keys are secret (for test masking).
type EnvLoader func(ctx context.Context, orgID string) (env map[string]string, secretKeys []string, err error)

// AccountLoader returns the account activation map for an event (optional).
type AccountLoader func(ctx context.Context, orgID, accountID string) (map[string]any, error)

// Repo is MongoDB access for routes, versions and rule errors.
type Repo struct {
	DB             *store.DB
	Engine         *Engine
	Cipher         *crypto.Cipher
	Jobs           *jobs.Service
	Audit          *audit.Writer
	EndpointExists EndpointExists
	EventLoader    EventLoader
	EnvLoader      EnvLoader
	AccountLoader  AccountLoader
	Lists          *ListCache
	Stats          *StatsRecorder
}

func (r *Repo) endpoints() EndpointExists {
	if r.EndpointExists != nil {
		return r.EndpointExists
	}
	return defaultEndpointExists(r.DB)
}

func (r *Repo) events() EventLoader {
	if r.EventLoader != nil {
		return r.EventLoader
	}
	return func(context.Context, string, string) (map[string]any, error) {
		return nil, errs.New(errs.CodeNotFound, "Event not found.")
	}
}

func defaultEndpointExists(db *store.DB) EndpointExists {
	return func(ctx context.Context, orgID, id string) (bool, error) {
		if db == nil {
			return false, nil
		}
		n, err := db.Collection(EndpointsCollection).CountDocuments(ctx, bson.M{"_id": id, "organizationId": orgID})
		return n > 0, err
	}
}

// Create inserts a route and its first version in one transaction.
func (r *Repo) Create(ctx context.Context, in RouteCreate) (*Route, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	orgID := c.OrganizationID
	name := strings.TrimSpace(in.Name)
	if name == "" || in.EndpointID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "name and endpointId are required")
	}
	ok, err := r.endpoints()(ctx, orgID, in.EndpointID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "Endpoint not found.").WithSafe("endpointId", in.EndpointID)
	}
	filter := in.Filter
	if in.Builder != nil {
		if b, berr := BuilderFromMap(in.Builder); berr == nil {
			if cel, cerr := BuilderToCEL(b); cerr == nil && strings.TrimSpace(filter) == "" {
				filter = cel
			}
		}
	}
	if strings.TrimSpace(filter) == "" {
		filter = "true"
	}
	if _, err := r.Engine.Compile(filter, in.Map); err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now().UTC()
	route := &Route{
		ID:             store.NewID(store.PrefixRoute),
		OrganizationID: orgID,
		Name:           name,
		Enabled:        enabled,
		EndpointID:     in.EndpointID,
		CurrentVersion: 1,
		Filter:         filter,
		Map:            in.Map,
		Builder:        in.Builder,
		Headers:        in.Headers,
		Payload:        in.Payload,
		Order:          in.Order,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if in.Secret != "" && r.Cipher != nil {
		es, err := r.Cipher.EncryptString(ctx, orgID, in.Secret, crypto.AAD{Collection: RoutesCollection, Field: "secret", DocID: route.ID})
		if err != nil {
			return nil, err
		}
		route.Secret = es
	}
	ver := &RouteVersion{
		ID: store.NewID(store.PrefixRouteVersion), OrganizationID: orgID, RouteID: route.ID,
		Version: 1, Filter: filter, Map: in.Map, Builder: in.Builder, CreatedBy: c.Actor(), CreatedAt: now,
	}
	err = r.DB.WithTx(ctx, func(sc context.Context) error {
		if _, err := r.DB.Collection(RoutesCollection).InsertOne(sc, route); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeRouteNameTaken, "A route with this name already exists.")
			}
			return err
		}
		_, err := r.DB.Collection(RouteVersionsCollection).InsertOne(sc, ver)
		return err
	})
	if err != nil {
		return nil, err
	}
	return route, nil
}

// Get returns a route by ID in the caller's organisation.
func (r *Repo) Get(ctx context.Context, id string) (*Route, error) {
	if err := tenancy.RequireReadOn(ctx, "routes", id); err != nil {
		return nil, err
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var route Route
	err = r.DB.Collection(RoutesCollection).FindOne(ctx, filter).Decode(&route)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Route not found.")
	}
	if err != nil {
		return nil, err
	}
	rows := []Route{route}
	if err := r.attachLastErrors(ctx, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// Update applies a merge patch; filter/map/builder changes bump the version in the same transaction.
func (r *Repo) Update(ctx context.Context, id string, in RouteUpdate, ifMatch string) (*Route, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	cur, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	exprChanged := false
	if in.Name != nil {
		cur.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.EndpointID != nil {
		ok, err := r.endpoints()(ctx, cur.OrganizationID, *in.EndpointID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errs.New(errs.CodeNotFound, "Endpoint not found.").WithSafe("endpointId", *in.EndpointID)
		}
		cur.EndpointID = *in.EndpointID
	}
	if in.Filter != nil {
		cur.Filter = *in.Filter
		exprChanged = true
	}
	if in.Map != nil {
		cur.Map = *in.Map
		exprChanged = true
	}
	if in.Builder != nil {
		cur.Builder = *in.Builder
		exprChanged = true
		if in.Filter == nil {
			if b, berr := BuilderFromMap(*in.Builder); berr == nil {
				if cel, cerr := BuilderToCEL(b); cerr == nil {
					cur.Filter = cel
				}
			}
		}
	}
	if in.Headers != nil {
		cur.Headers = *in.Headers
	}
	if in.Payload != nil {
		cur.Payload = *in.Payload
	}
	if in.Order != nil {
		cur.Order = *in.Order
	}
	if in.Secret != nil && r.Cipher != nil {
		if *in.Secret == "" {
			cur.Secret = crypto.EncryptedString{}
		} else {
			es, err := r.Cipher.EncryptString(ctx, cur.OrganizationID, *in.Secret, crypto.AAD{Collection: RoutesCollection, Field: "secret", DocID: cur.ID})
			if err != nil {
				return nil, err
			}
			cur.Secret = es
		}
	}
	if strings.TrimSpace(cur.Filter) == "" {
		cur.Filter = "true"
	}
	if _, err := r.Engine.Compile(cur.Filter, cur.Map); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	cur.UpdatedAt = now
	oldVersion := cur.Version
	cur.Version = oldVersion + 1
	var verDoc *RouteVersion
	if exprChanged {
		cur.CurrentVersion++
		verDoc = &RouteVersion{
			ID: store.NewID(store.PrefixRouteVersion), OrganizationID: cur.OrganizationID, RouteID: cur.ID,
			Version: cur.CurrentVersion, Filter: cur.Filter, Map: cur.Map, Builder: cur.Builder,
			CreatedBy: c.Actor(), CreatedAt: now,
		}
	}
	err = r.DB.WithTx(ctx, func(sc context.Context) error {
		filter, err := store.Scoped(sc, bson.M{"_id": id, "version": oldVersion})
		if err != nil {
			return err
		}
		res, err := r.DB.Collection(RoutesCollection).ReplaceOne(sc, filter, cur)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeRouteNameTaken, "A route with this name already exists.")
			}
			return err
		}
		if res.MatchedCount == 0 {
			return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
		}
		if verDoc != nil {
			_, err = r.DB.Collection(RouteVersionsCollection).InsertOne(sc, verDoc)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	r.Engine.InvalidateRoute(id)
	return cur, nil
}

// Delete removes a route.
func (r *Repo) Delete(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return err
	}
	cur, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if ifMatch != "" {
		filter["version"] = cur.Version
	}
	res, err := r.DB.Collection(RoutesCollection).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	r.Engine.InvalidateRoute(id)
	return nil
}

// WriteRuleError inserts a ruleErrors document with TTL 7 days.
func (r *Repo) WriteRuleError(ctx context.Context, e RuleError) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.ExpireAt = e.At.Add(7 * 24 * time.Hour)
	if e.ID == "" {
		e.ID = store.NewID(store.PrefixLog) // reuse log prefix space; rule errors are ephemeral
	}
	// Stored errors are shown in the admin (route errors): mask the organisation's secret env values first — a
	// CEL error can quote one (G3).
	if r.EnvLoader != nil {
		if env, secretKeys, err := r.EnvLoader(ctx, e.OrganizationID); err == nil {
			secrets := SecretValuesFromEnv(env, secretKeys)
			e.Message = MaskString(e.Message, secrets)
			e.PayloadExcerpt = MaskString(e.PayloadExcerpt, secrets)
		}
	}
	if len(e.PayloadExcerpt) > 4096 {
		e.PayloadExcerpt = e.PayloadExcerpt[:4096]
	}
	_, err := r.DB.Collection(RuleErrorsCollection).InsertOne(ctx, e)
	return err
}

// Test evaluates a route against a supplied or loaded event.
func (r *Repo) Test(ctx context.Context, id string, req RouteTestRequest) (*RouteTestResult, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	route, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.testProgram(ctx, route.OrganizationID, route.ID, route.CurrentVersion, route.Filter, route.Map, req)
}

// TestProgram evaluates filter/map with the same inputs as route/endpoint /test.
func (r *Repo) TestProgram(ctx context.Context, orgID, cacheKey string, version int, filter, mapExpr string, req RouteTestRequest) (*RouteTestResult, error) {
	return r.testProgram(ctx, orgID, cacheKey, version, filter, mapExpr, req)
}

func (r *Repo) testProgram(ctx context.Context, orgID, cacheKey string, version int, filter, mapExpr string, req RouteTestRequest) (*RouteTestResult, error) {
	event := req.Event
	var err error
	draft := req.IsDraft()
	if draft {
		if filter, mapExpr, err = DraftExprs(req); err != nil {
			return nil, err
		}
	}
	if event == nil && req.EventID != "" {
		event, err = r.events()(ctx, orgID, req.EventID)
		if err != nil {
			return nil, err
		}
	}
	if event == nil && req.Sample != "" {
		if event, err = SampleEvent(req.Sample); err != nil {
			return nil, err
		}
	}
	if event == nil {
		return nil, errs.New(errs.CodeValidationFailed, "event, eventId or sample is required")
	}
	account := map[string]any{}
	if r.AccountLoader != nil {
		aid, _ := event["ebaccount"].(string)
		if aid == "" {
			aid, _ = event["ebAccount"].(string)
		}
		if aid != "" {
			if a, aerr := r.AccountLoader(ctx, orgID, aid); aerr == nil && a != nil {
				account = a
			}
		}
	}
	env := map[string]string{}
	var secretKeys []string
	if r.EnvLoader != nil {
		env, secretKeys, _ = r.EnvLoader(ctx, orgID)
	}
	secrets := SecretValuesFromEnv(env, secretKeys)
	var lists map[string]map[string]struct{}
	if r.Lists != nil {
		lists, _ = r.Lists.Snapshot(ctx, orgID)
	}
	var prg *Program
	if draft { // never cached: a draft must not replace (or be served from) the router's program
		prg, err = r.Engine.Compile(filter, mapExpr)
	} else {
		prg, err = r.Engine.Cached(cacheKey, version, filter, mapExpr)
	}
	if err != nil {
		return &RouteTestResult{Error: MaskString(err.Error(), secrets), Trace: nil, ErrorAt: ErrorPositionOf(err)}, nil
	}
	start := time.Now()
	matched, mapped, cost, evalErr := r.Engine.EvalWithLists(ctx, prg, event, account, env, lists)
	dur := time.Since(start).Milliseconds()
	trace := r.Engine.BuildTrace(ctx, filter, event, account, env, lists)
	out := &RouteTestResult{Matched: matched, CostUsed: cost, Cost: cost, DurationMs: dur, Trace: trace}
	if evalErr != nil {
		out.Error = MaskString(evalErr.Error(), secrets)
		return out, nil
	}
	if matched {
		out.Mapped = MaskSecrets(mapped, secrets)
	}
	return out, nil
}

// DraftExprs resolves the draft filter / map of a test request (W73b). An empty draft filter falls back to the
// draft builder, then to "true" (an endpoint rule with only a map matches every subscribed event).
func DraftExprs(req RouteTestRequest) (filter, mapExpr string, err error) {
	if req.Filter != nil {
		filter = strings.TrimSpace(*req.Filter)
	}
	if filter == "" && req.Builder != nil {
		if filter, err = BuilderToCEL(*req.Builder); err != nil {
			return "", "", err
		}
	}
	if filter == "" {
		filter = "true"
	}
	if req.Map != nil {
		mapExpr = strings.TrimSpace(*req.Map)
	}
	return filter, mapExpr, nil
}

// ErrorPositionOf returns where a compile error sits, or nil when err carries no position.
func ErrorPositionOf(err error) *ErrorPosition {
	var e *errs.E
	if !errors.As(err, &e) || e.Code != errs.CodeRouteCompileError {
		return nil
	}
	path, _ := e.Safe["path"].(string)
	line, _ := e.Safe["line"].(int)
	col, _ := e.Safe["column"].(int)
	if path == "" {
		return nil
	}
	return &ErrorPosition{Path: path, Line: line, Column: col}
}

func parseETag(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	var n int64
	_, _ = fmt.Sscanf(s, "v%d", &n)
	if n == 0 {
		_, _ = fmt.Sscanf(s, "%d", &n)
	}
	return n
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

// EnsureIndexes creates route-related indexes (also used by Migration).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, RoutesCollection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
	); err != nil {
		return err
	}
	if err := db.EnsureIndexes(ctx, RouteVersionsCollection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "routeId", Value: 1}, {Key: "version", Value: -1}},
			Options: options.Index().SetUnique(true),
		},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, RuleErrorsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "routeId", Value: 1}, {Key: "at", Value: -1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "expireAt", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0),
		},
	)
}
