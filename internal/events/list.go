package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
	totalListCap     = 10_000
	catchUpBatch     = 500
	resumeSkew       = 5 * time.Second
)

// ListFilter holds GET /v1/events query parameters.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Type         []string // exact types and/or "<family>.*" wildcards
	AccountID    string
	From         time.Time
	To           time.Time
}

// List is GET /v1/events.
type List struct {
	Data        []Event `json:"data" doc:"The events of this page."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page."`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching events (only with page and includeTotal; capped at 10 000)."`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000."`
}

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// List returns organisation-scoped CloudEvents envelopes.
func (w *Writer) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "events"); err != nil {
		return nil, err
	}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if f.AccountID != "" && f.AccountID != c.AccountID {
			return nil, errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
		}
		f.AccountID = c.AccountID
	}
	match, err := listMatch(f)
	if err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, match)
	if err != nil {
		return nil, err
	}
	field, dir, err := eventSort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := listLimit(f.Limit)
	filter, opts, err := listQuery(base, field, dir, limit, f.Cursor, f.Page)
	if err != nil {
		return nil, err
	}
	cur, err := w.DB.Collection(Collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "events.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []doc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "events.list.decode")
	}
	out := &List{Data: make([]Event, 0, len(docs))}
	for _, d := range docs {
		out.Data = append(out.Data, d.event())
	}
	if len(out.Data) > limit {
		out.Data = out.Data[:limit]
		value := eventSortKey(docs[limit-1], field)
		token := encodeListCursor(listCursor{ID: docs[limit-1].ID, Key: value, Field: field, Desc: dir < 0})
		out.NextCursor = &token
	}
	out.Total, out.TotalCapped, err = w.listTotal(ctx, base, f.IncludeTotal && f.Page > 0)
	return out, err
}

// GetEvent is Get with PermRead (API). Writer.Get remains the raw store read used by the relay/sinks.
func (w *Writer) GetEvent(ctx context.Context, id string) (Event, error) {
	if err := tenancy.RequireRead(ctx, "events"); err != nil {
		return Event{}, err
	}
	e, err := w.Get(ctx, id)
	if err != nil {
		return Event{}, err
	}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" && e.EBAccount != c.AccountID {
		return Event{}, errs.New(errs.CodeNotFound, "The event does not exist.")
	}
	return e, nil
}

func (w *Writer) listTotal(ctx context.Context, base bson.M, include bool) (*int64, *bool, error) {
	if !include {
		return nil, nil, nil
	}
	n, err := w.DB.Collection(Collection).CountDocuments(ctx, base, options.Count().SetLimit(totalListCap+1))
	if err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "events.list.total")
	}
	capped := n > totalListCap
	if capped {
		n = totalListCap
	}
	return &n, &capped, nil
}

// CatchUp returns stored events with time ≥ since, in _id ascending order, after afterID (exclusive when set).
func (w *Writer) CatchUp(ctx context.Context, since time.Time, afterID string, types []string, accountID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = catchUpBatch
	}
	match := bson.M{"time": bson.M{"$gte": since.UTC()}}
	if afterID != "" {
		match["_id"] = bson.M{"$gt": afterID}
	}
	if accountID != "" {
		match["accountId"] = accountID
	}
	tf, err := typeFilter(types)
	if err != nil {
		return nil, err
	}
	for k, v := range tf {
		match[k] = v
	}
	base, err := store.Scoped(ctx, match)
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := w.DB.Collection(Collection).Find(ctx, base, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "events.catchup")
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []doc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "events.catchup.decode")
	}
	out := make([]Event, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.event())
	}
	return out, nil
}

func listMatch(f ListFilter) (bson.M, error) {
	filter := bson.M{}
	if f.AccountID != "" {
		if !store.IsID(store.PrefixAccount, f.AccountID) {
			return nil, errs.New(errs.CodeValidationFailed, "accountId is not a valid account id.")
		}
		filter["accountId"] = f.AccountID
	}
	tf, err := typeFilter(f.Type)
	if err != nil {
		return nil, err
	}
	for k, v := range tf {
		filter[k] = v
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		rng := bson.M{}
		if !f.From.IsZero() {
			rng["$gte"] = f.From.UTC()
		}
		if !f.To.IsZero() {
			rng["$lte"] = f.To.UTC()
		}
		filter["time"] = rng
	}
	return filter, nil
}

// typeFilter builds a Mongo filter for type values. A value ending in ".*" matches the family prefix
// (e.g. message.* → type /^message\./). Exact and wildcard entries may be mixed.
func typeFilter(raw []string) (bson.M, error) {
	types := normalizeTypes(raw)
	if len(types) == 0 {
		return nil, nil
	}
	var exact []string
	var prefixes []string
	for _, t := range types {
		if strings.HasSuffix(t, ".*") {
			family := strings.TrimSuffix(t, ".*")
			if family == "" || strings.Contains(family, ".") || strings.Contains(family, "*") {
				return nil, errs.New(errs.CodeValidationFailed, "type wildcards must be <family>.* (one token).").WithSafe("type", t)
			}
			prefixes = append(prefixes, family+".")
			continue
		}
		if strings.Contains(t, "*") {
			return nil, errs.New(errs.CodeValidationFailed, "type wildcards must be <family>.* (one token).").WithSafe("type", t)
		}
		exact = append(exact, t)
	}
	clauses := make([]bson.M, 0, len(exact)+len(prefixes))
	if len(exact) == 1 && len(prefixes) == 0 {
		return bson.M{"type": exact[0]}, nil
	}
	if len(exact) > 1 && len(prefixes) == 0 {
		return bson.M{"type": bson.M{"$in": exact}}, nil
	}
	for _, e := range exact {
		clauses = append(clauses, bson.M{"type": e})
	}
	for _, p := range prefixes {
		clauses = append(clauses, bson.M{"type": bson.M{"$regex": "^" + regexpQuote(p)}})
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	return bson.M{"$or": clauses}, nil
}

func normalizeTypes(raw []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '\\', '^', '$':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
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

func eventSort(value string) (string, int, error) {
	if value == "" {
		value = "-time"
	}
	desc := strings.HasPrefix(value, "-")
	name := strings.TrimPrefix(value, "-")
	switch name {
	case "time", "id":
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
	if field == "time" {
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

func eventSortKey(d doc, field string) any {
	switch field {
	case "time":
		return d.Time.UTC().Format(time.RFC3339Nano)
	default:
		return d.ID
	}
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

// ListIndexesMigration adds indexes for time-range and account+time listings (P3.3).
func ListIndexesMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "events list indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "time", Value: -1}, {Key: "_id", Value: -1}},
				Options: options.Index().SetName("org_time_id"),
			},
			mongo.IndexModel{
				Keys: bson.D{
					{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1},
					{Key: "time", Value: -1}, {Key: "_id", Value: -1},
				},
				Options: options.Index().SetName("org_account_time_id"),
			},
		)
	}}
}
