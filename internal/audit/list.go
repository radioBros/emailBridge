package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// ListFilter holds GET /v1/audit-log query parameters.
type ListFilter struct {
	ActorID      string
	TargetID     string
	Action       string
	From, To     *time.Time
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string // "at", "-at", "id", "-id"; default "-at"
}

// ListResult is a cursor/page list response.
type ListResult struct {
	Data        []Record `json:"data" doc:"Audit records of this page."`
	NextCursor  *string  `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJhdCI6IjIwMjYtMTAtMjRUMTQ6MzI6MTBaIiwiaWQiOiJhdWRfMDFtNWQ0NHA3MGh3bjN3NzZuM2Z6cjJnYWEiLCJkZXNjIjp0cnVlLCJieUF0Ijp0cnVlfQ"`
	Total       *int64   `json:"total,omitempty" doc:"Total matching records (only with page and includeTotal)." example:"1342"`
	TotalCapped *bool    `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

type cursorPayload struct {
	At string `json:"at,omitempty"`
	ID string `json:"id"`
	// Desc is true when sorting descending on the primary key.
	Desc bool `json:"desc"`
	ByAt bool `json:"byAt"`
}

// List returns audit records visible to the caller (tenant isolation + instance-level for owners).
func (w *Writer) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermAuditRead); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	filter, err := buildFilter(ctx, c, f)
	if err != nil {
		return nil, err
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

	if f.Page > 0 {
		skip := int64(f.Page-1) * int64(limit)
		findOpts.SetSkip(skip)
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := cursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{filter, keyFilter}}
	}

	cur, err := w.DB.Collection(Collection).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var rows []Record
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("audit: list decode: %w", err)
	}
	out := &ListResult{Data: rows}
	if out.Data == nil {
		out.Data = []Record{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{
			At:   last.At.UTC().Format(time.RFC3339Nano),
			ID:   last.ID,
			Desc: sortDir < 0,
			ByAt: sortField == "at",
		})
		out.NextCursor = &nc
	} else {
		out.NextCursor = nil
	}

	if f.IncludeTotal && f.Page > 0 {
		n, err := w.DB.Collection(Collection).CountDocuments(ctx, filter, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, fmt.Errorf("audit: count: %w", err)
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

func buildFilter(ctx context.Context, c tenancy.Caller, f ListFilter) (bson.M, error) {
	base := bson.M{}
	if f.ActorID != "" {
		base["actor.id"] = f.ActorID
	}
	if f.TargetID != "" {
		base["target.id"] = f.TargetID
	}
	if f.Action != "" {
		base["action"] = f.Action
	}
	if f.From != nil || f.To != nil {
		rng := bson.M{}
		if f.From != nil {
			rng["$gte"] = f.From.UTC()
		}
		if f.To != nil {
			rng["$lte"] = f.To.UTC()
		}
		base["at"] = rng
	}

	scoped, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		// Instance owner with no organisation: only instance-level records.
		if c.InstanceOwner {
			base["organizationId"] = nil
			return base, nil
		}
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if c.InstanceOwner {
		return bson.M{"$and": []bson.M{
			base,
			{"$or": []bson.M{scoped, {"organizationId": nil}}},
		}}, nil
	}
	for k, v := range scoped {
		base[k] = v
	}
	return base, nil
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-at"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	switch name {
	case "at", "id":
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
	at, err := time.Parse(time.RFC3339Nano, p.At)
	if err != nil {
		at, err = time.Parse(time.RFC3339, p.At)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
	}
	// (at, _id) keyset: at < cursor.at OR (at == cursor.at AND _id < cursor.id) when descending.
	return bson.M{"$or": []bson.M{
		{"at": bson.M{cmp: at}},
		{"at": at, "_id": bson.M{cmp: p.ID}},
	}}, nil
}
