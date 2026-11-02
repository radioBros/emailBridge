package rules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	totalCap     = 10_000
)

// ListFilter holds GET /v1/routes query parameters.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string // name, -name, order, -order, createdAt, -createdAt, id, -id; default -id
	Q            string // case-insensitive substring on name
}

type cursorPayload struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// List returns routes for the caller's organisation.
func (r *Repo) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	if err := tenancy.RequireRead(ctx, "routes"); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if ids, limited := tenancy.ScopedIDs(ctx, "routes"); limited {
		base["_id"] = bson.M{"$in": ids}
	}
	if q, err := normalizeListQ(f.Q); err != nil {
		return nil, err
	} else if q != "" {
		base["name"] = bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
	}
	sortField, sortDir, err := parseSort(f.Sort)
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
	findOpts := options.Find().SetLimit(int64(limit + 1))
	sortDoc := bson.D{{Key: sortField, Value: sortDir}}
	if sortField != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: sortDir})
	}
	findOpts.SetSort(sortDoc)

	filter := base
	if f.Page > 0 {
		findOpts.SetSkip(int64(f.Page-1) * int64(limit))
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := cursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}

	cur, err := r.DB.Collection(RoutesCollection).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("rules: list: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var rows []Route
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("rules: list decode: %w", err)
	}
	out := &ListResult{Data: rows}
	if out.Data == nil {
		out.Data = []Route{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{
			ID:    last.ID,
			Field: sortField,
			Desc:  sortDir < 0,
			Key:   sortKey(last, sortField),
		})
		out.NextCursor = &nc
	}
	if err := r.attachLastErrors(ctx, out.Data); err != nil {
		return nil, err
	}

	if f.IncludeTotal && f.Page > 0 {
		n, err := r.DB.Collection(RoutesCollection).CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, err
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total = &n
		out.TotalCapped = &capped
	}
	return out, nil
}

func normalizeListQ(q string) (string, error) {
	q = strings.TrimSpace(q)
	if len(q) > 200 {
		return "", errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
	}
	return q, nil
}

// attachLastErrors fills LastError from the newest ruleErrors row per route (one aggregation).
func (r *Repo) attachLastErrors(ctx context.Context, routes []Route) error {
	if len(routes) == 0 {
		return nil
	}
	ids := make([]string, len(routes))
	orgID := routes[0].OrganizationID
	for i := range routes {
		ids[i] = routes[i].ID
	}
	last, err := r.lastErrors(ctx, orgID, ids)
	if err != nil {
		return err
	}
	for i := range routes {
		if le, ok := last[routes[i].ID]; ok {
			cp := le
			routes[i].LastError = &cp
		}
	}
	return nil
}

func (r *Repo) lastErrors(ctx context.Context, orgID string, routeIDs []string) (map[string]RouteLastError, error) {
	out := map[string]RouteLastError{}
	if orgID == "" || len(routeIDs) == 0 {
		return out, nil
	}
	pipe := []bson.M{
		{"$match": bson.M{"organizationId": orgID, "routeId": bson.M{"$in": routeIDs}}},
		// Index order {organizationId, routeId, _id: -1}: newest error per route first, no in-memory sort.
		{"$sort": bson.D{{Key: "organizationId", Value: 1}, {Key: "routeId", Value: 1}, {Key: "_id", Value: -1}}},
		{"$group": bson.M{
			"_id":     "$routeId",
			"phase":   bson.M{"$first": "$phase"},
			"message": bson.M{"$first": "$message"},
			"at":      bson.M{"$first": "$at"},
		}},
	}
	cur, err := r.DB.Collection(RuleErrorsCollection).Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []struct {
		RouteID string    `bson:"_id"`
		Phase   string    `bson:"phase"`
		Message string    `bson:"message"`
		At      time.Time `bson:"at"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.RouteID] = RouteLastError{Code: row.Phase, Message: row.Message, At: row.At}
	}
	return out, nil
}

func sortKey(r Route, field string) any {
	switch field {
	case "name":
		return r.Name
	case "order":
		return r.Order
	case "createdAt":
		return r.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return r.ID
	}
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-id"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	switch name {
	case "name", "order", "createdAt", "id":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", s)
	}
	field = name
	if name == "id" {
		field = "_id"
	}
	dir = 1
	if desc {
		dir = -1
	}
	return field, dir, nil
}

func encodeCursor(p cursorPayload) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return cursorPayload{}, err
	}
	if p.ID == "" {
		return cursorPayload{}, fmt.Errorf("empty id")
	}
	return p, nil
}

func cursorFilter(p cursorPayload, sortField string, sortDir int) (bson.M, error) {
	cmp := "$lt"
	if sortDir > 0 {
		cmp = "$gt"
	}
	if sortField == "_id" {
		return bson.M{"_id": bson.M{cmp: p.ID}}, nil
	}
	key := p.Key
	if sortField == "createdAt" {
		if s, ok := key.(string); ok {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				t, err = time.Parse(time.RFC3339, s)
				if err != nil {
					return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
				}
			}
			key = t
		}
	}
	return bson.M{"$or": []bson.M{
		{sortField: bson.M{cmp: key}},
		{sortField: key, "_id": bson.M{cmp: p.ID}},
	}}, nil
}
