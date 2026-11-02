package webhook

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
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
	totalListCap     = 10_000
)

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// EndpointListFilter holds GET /v1/webhook-endpoints query parameters.
type EndpointListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Q            string
	IDs          []string // optional allow-list (P3.4a granular keys)
}

func (r *Repo) listEndpoints(ctx context.Context, f EndpointListFilter) ([]Endpoint, *string, *int64, *bool, error) {
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if f.IDs != nil {
		base["_id"] = bson.M{"$in": f.IDs}
	}
	if q, err := normalizeListQ(f.Q); err != nil {
		return nil, nil, nil, nil, err
	} else if q != "" {
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		base["$or"] = []bson.M{{"name": rx}, {"url": rx}}
	}
	field, dir, err := endpointSort(f.Sort)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	limit := listLimit(f.Limit)
	filter, opts, err := listQuery(base, field, dir, limit, f.Cursor, f.Page)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cur, err := r.DB.Collection(EndpointsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Endpoint
	if err := cur.All(ctx, &rows); err != nil {
		return nil, nil, nil, nil, err
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		value := endpointSortKey(rows[len(rows)-1], field)
		token := encodeListCursor(listCursor{ID: rows[len(rows)-1].ID, Key: value, Field: field, Desc: dir < 0})
		next = &token
	}
	total, capped, err := r.listTotal(ctx, EndpointsCollection, base, f.IncludeTotal && f.Page > 0)
	return rows, next, total, capped, err
}

func (r *Repo) listDeliveries(ctx context.Context, f DeliveryFilter) (*DeliveryList, error) {
	match := deliveryMatch(f)
	if q, err := normalizeListQ(f.Q); err != nil {
		return nil, err
	} else if q != "" {
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		match["$or"] = []bson.M{{"eventType": rx}, {"endpointId": rx}}
	}
	base, err := store.Scoped(ctx, match)
	if err != nil {
		return nil, err
	}
	field, dir, err := deliverySort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := listLimit(f.Limit)
	filter, opts, err := listQuery(base, field, dir, limit, f.Cursor, f.Page)
	if err != nil {
		return nil, err
	}
	cur, err := r.DB.Collection(DeliveriesCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Delivery
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := &DeliveryList{Data: rows}
	if out.Data == nil {
		out.Data = []Delivery{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		value := deliverySortKey(rows[len(rows)-1], field)
		token := encodeListCursor(listCursor{ID: rows[len(rows)-1].ID, Key: value, Field: field, Desc: dir < 0})
		out.NextCursor = &token
	}
	out.Total, out.TotalCapped, err = r.listTotal(ctx, DeliveriesCollection, base, f.IncludeTotal && f.Page > 0)
	return out, err
}

func normalizeListQ(q string) (string, error) {
	q = strings.TrimSpace(q)
	if len(q) > 200 {
		return "", errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
	}
	return q, nil
}

// lastDeliveries returns the newest delivery summary per endpoint (one aggregation).
func (r *Repo) lastDeliveries(ctx context.Context, orgID string, endpointIDs []string) (map[string]LastDelivery, error) {
	out := map[string]LastDelivery{}
	if orgID == "" || len(endpointIDs) == 0 {
		return out, nil
	}
	pipe := []bson.M{
		{"$match": bson.M{"organizationId": orgID, "endpointId": bson.M{"$in": endpointIDs}}},
		// Sort in index order {organizationId, endpointId, _id: -1} so the newest row per endpoint comes first
		// without an in-memory sort of every delivery of the page's endpoints.
		{"$sort": bson.D{{Key: "organizationId", Value: 1}, {Key: "endpointId", Value: 1}, {Key: "_id", Value: -1}}},
		{"$group": bson.M{
			"_id":       "$endpointId",
			"state":     bson.M{"$first": "$state"},
			"createdAt": bson.M{"$first": "$createdAt"},
			"attempts":  bson.M{"$first": "$attempts"},
		}},
	}
	cur, err := r.DB.Collection(DeliveriesCollection).Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []struct {
		EndpointID string            `bson:"_id"`
		State      string            `bson:"state"`
		CreatedAt  time.Time         `bson:"createdAt"`
		Attempts   []DeliveryAttempt `bson:"attempts"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		ld := LastDelivery{State: row.State, At: row.CreatedAt}
		if n := len(row.Attempts); n > 0 {
			last := row.Attempts[n-1]
			ld.At = last.At
			if last.StatusCode != 0 {
				ld.StatusCode = last.StatusCode
			}
		}
		out[row.EndpointID] = ld
	}
	return out, nil
}

func listLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
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
	if field == "createdAt" || field == "lastAttemptAt" {
		value, ok := key.(string)
		if !ok {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		key, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
	}
	rangeFilter := bson.M{"$or": []bson.M{
		{field: bson.M{cmp: key}},
		{field: key, "_id": bson.M{cmp: decoded.ID}},
	}}
	return bson.M{"$and": []bson.M{base, rangeFilter}}, opts, nil
}

func (r *Repo) listTotal(ctx context.Context, collection string, base bson.M, include bool) (*int64, *bool, error) {
	if !include {
		return nil, nil, nil
	}
	n, err := r.DB.Collection(collection).CountDocuments(ctx, base, options.Count().SetLimit(totalListCap+1))
	if err != nil {
		return nil, nil, err
	}
	capped := n > totalListCap
	if capped {
		n = totalListCap
	}
	return &n, &capped, nil
}

func endpointSort(value string) (string, int, error) {
	if value == "" {
		value = "-id"
	}
	desc := strings.HasPrefix(value, "-")
	name := strings.TrimPrefix(value, "-")
	switch name {
	case "id", "name", "createdAt":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", value)
	}
	if name == "id" {
		name = "_id"
	}
	if desc {
		return name, -1, nil
	}
	return name, 1, nil
}

func deliverySort(value string) (string, int, error) {
	if value == "" {
		value = "-id"
	}
	desc := strings.HasPrefix(value, "-")
	name := strings.TrimPrefix(value, "-")
	switch name {
	case "id", "createdAt", "state", "lastAttemptAt":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", value)
	}
	if name == "id" {
		name = "_id"
	}
	if desc {
		return name, -1, nil
	}
	return name, 1, nil
}

func endpointSortKey(endpoint Endpoint, field string) any {
	switch field {
	case "name":
		return endpoint.Name
	case "createdAt":
		return endpoint.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return endpoint.ID
	}
}

func deliverySortKey(delivery Delivery, field string) any {
	switch field {
	case "state":
		return delivery.State
	case "createdAt":
		return delivery.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "lastAttemptAt":
		if delivery.LastAttemptAt != nil {
			return delivery.LastAttemptAt.UTC().Format(time.RFC3339Nano)
		}
		return time.Time{}.UTC().Format(time.RFC3339Nano)
	default:
		return delivery.ID
	}
}

func deliveryMatch(f DeliveryFilter) bson.M {
	filter := bson.M{}
	for key, value := range map[string]string{
		"endpointId": f.EndpointID,
		"routeId":    f.RouteID,
		"eventId":    f.EventID,
		"eventType":  f.EventType,
		"accountId":  f.AccountID,
	} {
		if value != "" {
			filter[key] = value
		}
	}
	if len(f.EndpointIDs) > 0 {
		if f.EndpointID != "" {
			filter["endpointId"] = f.EndpointID // single id still wins when also allowed
		} else {
			filter["endpointId"] = bson.M{"$in": f.EndpointIDs}
		}
	} else if f.EndpointIDs != nil {
		filter["endpointId"] = bson.M{"$in": []string{}}
	}
	if states := splitDeliveryStates(f.State); len(states) == 1 {
		filter["state"] = states[0]
	} else if len(states) > 1 {
		filter["state"] = bson.M{"$in": states}
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		rng := bson.M{}
		if !f.From.IsZero() {
			rng["$gte"] = f.From
		}
		if !f.To.IsZero() {
			rng["$lte"] = f.To
		}
		filter["createdAt"] = rng
	}
	return filter
}

func splitDeliveryStates(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
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
		return listCursor{}, fmt.Errorf("empty cursor")
	}
	return cursor, nil
}
