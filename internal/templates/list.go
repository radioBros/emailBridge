package templates

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

type cursorPayload struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// List returns templates for the caller's organisation.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "templates"); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
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
	// Lists carry the sources but not the compiled MJML output (up to 1 MiB each); GET has it.
	findOpts := options.Find().SetLimit(int64(limit + 1)).SetProjection(bson.M{"htmlCompiled": 0})
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

	cur, err := s.Repo.col().Find(ctx, filter, findOpts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "templates.list")
	}
	defer func() { _ = cur.Close(ctx) }()

	var rows []Template
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "templates.list.decode")
	}
	for i := range rows {
		rows[i].HTMLFormat = formatOf(rows[i].HTMLFormat)
	}
	out := &List{Data: rows}
	if out.Data == nil {
		out.Data = []Template{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{
			ID: last.ID, Field: sortField, Desc: sortDir < 0, Key: sortKey(last, sortField),
		})
		out.NextCursor = &nc
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := s.Repo.col().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "templates.list.count")
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

func sortKey(t Template, field string) any {
	switch field {
	case "name":
		return t.Name
	case "createdAt":
		return t.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return t.ID
	}
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-id"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	switch name {
	case "name", "createdAt", "id":
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
	raw, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (cursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, err
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return cursorPayload{}, err
	}
	if p.ID == "" || p.Field == "" {
		return cursorPayload{}, fmt.Errorf("incomplete cursor")
	}
	return p, nil
}

func cursorFilter(cur cursorPayload, sortField string, sortDir int) (bson.M, error) {
	op := "$lt"
	if sortDir > 0 {
		op = "$gt"
	}
	if sortField == "_id" || cur.Field == "_id" || cur.Field == "id" {
		return bson.M{"_id": bson.M{op: cur.ID}}, nil
	}
	if cur.Key == nil {
		return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
	}
	return bson.M{"$or": []bson.M{
		{sortField: bson.M{op: cur.Key}},
		{sortField: cur.Key, "_id": bson.M{op: cur.ID}},
	}}, nil
}
