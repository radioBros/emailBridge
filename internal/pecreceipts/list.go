package pecreceipts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	totalCap     = 10_000
	maxQLen      = 200
)

// ListFilter is GET /v1/accounts/{accountId}/pec/receipts.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Type         string
	OutboundID   string
	Q            string
}

// List is the list response body.
type List struct {
	Data        []Receipt `json:"data" doc:"The receipts of this page."`
	NextCursor  *string   `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6InJjcF8wMW01ZDIweTgwbjRwczBhcHozZTZiYXFxMyIsImZpZWxkIjoiYXQiLCJkZXNjIjp0cnVlfQ"`
	Total       *int64    `json:"total,omitempty" doc:"Total number of matching receipts (only with page and includeTotal; capped at 10 000)." example:"2"`
	TotalCapped *bool     `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// List returns a page of receipts for the account (caller must already have checked scope + feature).
func (r *Repo) List(ctx context.Context, accountID string, f ListFilter) (*List, error) {
	base, err := store.Scoped(ctx, bson.M{"accountId": accountID})
	if err != nil {
		return nil, err
	}
	if typ := strings.TrimSpace(f.Type); typ != "" {
		base["type"] = typ
	}
	if out := strings.TrimSpace(f.OutboundID); out != "" {
		if !store.IsID(store.PrefixOutbound, out) {
			return nil, errs.New(errs.CodeValidationFailed, "outboundId is not a valid outbound id.")
		}
		base["outboundId"] = out
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if len(q) > maxQLen {
			return nil, errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
		}
		rx := bson.M{"$regex": "^" + regexp.QuoteMeta(q), "$options": "i"}
		base["$or"] = []bson.M{{"transportId": rx}, {"deliveredTo": rx}}
	}
	field, dir, err := parseSort(f.Sort)
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
	opts := options.Find().SetLimit(int64(limit + 1))
	sortDoc := bson.D{{Key: field, Value: dir}}
	if field != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: dir})
	}
	opts.SetSort(sortDoc)
	filter := base
	if f.Page > 0 {
		opts.SetSkip(int64(f.Page-1) * int64(limit))
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := cursorFilter(cur, field, dir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}
	cur, err := r.coll().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Receipt
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.list")
	}
	out := &List{Data: []Receipt{}}
	if len(rows) > limit {
		rows = rows[:limit]
		token := encodeCursor(listCursor{
			ID: rows[len(rows)-1].ID, Key: sortKey(rows[len(rows)-1], field), Field: field, Desc: dir < 0,
		})
		out.NextCursor = &token
	}
	out.Data = rows
	if f.IncludeTotal && f.Page > 0 {
		n, err := r.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.list")
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-at"
	}
	dir = 1
	if strings.HasPrefix(s, "-") {
		dir = -1
		s = s[1:]
	}
	switch s {
	case "at":
		return "at", dir, nil
	case "type":
		return "type", dir, nil
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", s)
	}
}

func sortKey(rc Receipt, field string) any {
	switch field {
	case "at":
		return rc.At.UTC().Format(time.RFC3339Nano)
	case "type":
		return rc.Type
	default:
		return rc.ID
	}
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

func cursorFilter(c listCursor, field string, dir int) (bson.M, error) {
	if c.Field != field {
		return nil, errs.New(errs.CodeValidationFailed, "The cursor does not match the sort.")
	}
	op := "$gt"
	if dir < 0 || c.Desc {
		op = "$lt"
	}
	if field == "_id" {
		return bson.M{"_id": bson.M{op: c.ID}}, nil
	}
	key := c.Key
	if field == "at" {
		t, err := parseCursorTime(key)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		key = t
	}
	return bson.M{"$or": []bson.M{
		{field: bson.M{op: key}},
		{field: key, "_id": bson.M{op: c.ID}},
	}}, nil
}

func parseCursorTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return parsed, nil
		}
		return time.Parse(time.RFC3339, t)
	default:
		return time.Time{}, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
	}
}
