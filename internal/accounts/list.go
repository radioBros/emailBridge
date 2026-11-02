package accounts

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

type cursorPayload struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// List returns accounts for the caller's organisation (S2 §2).
func (s *Service) List(ctx context.Context, f ListFilter) (*AccountList, error) {
	if err := tenancy.RequireRead(ctx, "accounts"); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.scoped")
	}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		base["_id"] = c.AccountID
	}
	if ids, limited := tenancy.ScopedIDs(ctx, "accounts"); limited {
		if existing, ok := base["_id"].(string); ok {
			if !containsID(ids, existing) {
				base["_id"] = bson.M{"$in": []string{}}
			}
		} else {
			base["_id"] = bson.M{"$in": ids}
		}
	}
	if f.State != "" {
		valid := false
		for _, st := range AllStates {
			if st == f.State {
				valid = true
				break
			}
		}
		if !valid {
			return nil, errs.New(errs.CodeValidationFailed, "Unknown account state.").WithSafe("state", f.State)
		}
		base["state"] = f.State
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		base["$or"] = []bson.M{
			{"name": bson.M{"$regex": "^" + escapeRegex(q), "$options": "i"}},
			{"email": bson.M{"$regex": "^" + escapeRegex(q), "$options": "i"}},
			{"externalId": bson.M{"$regex": "^" + escapeRegex(q), "$options": "i"}},
		}
	}
	metaCond, err := metadataListFilter(f.Metadata)
	if err != nil {
		return nil, err
	}
	for k, v := range metaCond { // a single "$expr" key; base never holds one
		base[k] = v
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

	cur, err := s.coll().Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("accounts: list: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var rows []Account
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("accounts: list decode: %w", err)
	}
	out := &AccountList{Data: rows}
	if out.Data == nil {
		out.Data = []Account{}
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
		n, err := s.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
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

func escapeRegex(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`, `.`, `\.`, `+`, `\+`, `*`, `\*`, `?`, `\?`,
		`(`, `\(`, `)`, `\)`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`,
		`^`, `\^`, `$`, `\$`, `|`, `\|`,
	)
	return replacer.Replace(s)
}

func sortKey(a Account, field string) any {
	switch field {
	case "name":
		return a.Name
	case "email":
		return a.Email
	case "state":
		return a.State
	case "createdAt":
		return a.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "lastSyncAt":
		if a.LastSyncAt == nil {
			return nil
		}
		return a.LastSyncAt.UTC().Format(time.RFC3339Nano)
	default:
		return a.ID
	}
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-id"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	switch name {
	case "name", "email", "state", "createdAt", "lastSyncAt", "id":
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

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
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
	if sortField == "createdAt" || sortField == "lastSyncAt" {
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
