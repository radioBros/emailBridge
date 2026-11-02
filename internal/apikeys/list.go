package apikeys

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

// List returns API keys for the caller's organisation (S2 §2).
func (s *Service) List(ctx context.Context, f ListFilter) (*KeyList, error) {
	if err := tenancy.RequireRead(ctx, "apikeys"); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if q, err := normalizeListQ(f.Q); err != nil {
		return nil, err
	} else if q != "" {
		rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		base["$or"] = []bson.M{{"name": rx}, {"prefix": rx}}
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

	cur, err := s.DB.Collection(Collection).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("apikeys: list: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var rows []Key
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("apikeys: list decode: %w", err)
	}
	out := &KeyList{Data: rows}
	if out.Data == nil {
		out.Data = []Key{}
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
		n, err := s.DB.Collection(Collection).CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, err
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
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

func sortKey(k Key, field string) any {
	switch field {
	case "name":
		return k.Name
	case "createdAt":
		return k.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "lastUsedAt":
		if k.LastUsedAt == nil {
			return nil
		}
		return k.LastUsedAt.UTC().Format(time.RFC3339Nano)
	default:
		return k.ID
	}
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-id"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	switch name {
	case "name", "createdAt", "lastUsedAt", "id":
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
	if sortField == "createdAt" || sortField == "lastUsedAt" {
		if s, ok := key.(string); ok && s != "" {
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
