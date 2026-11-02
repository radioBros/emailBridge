package submit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// listCursor is the opaque keyset cursor of S2 §2.
type listCursor struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func (r *Repo) list(ctx context.Context, f ListFilter) (*List, error) {
	base, err := store.Scoped(ctx, listMatch(f))
	if err != nil {
		return nil, err
	}
	field, dir, err := listSort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := listLimit(f.Limit)
	filter, opts, err := listQuery(base, field, dir, limit, f.Cursor, f.Page)
	if err != nil {
		return nil, err
	}
	cur, err := r.coll().Find(ctx, filter, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.list")
	}
	rows, err := collect(ctx, cur)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.list")
	}
	out := &List{Data: []View{}}
	if len(rows) > limit {
		rows = rows[:limit]
		token := encodeListCursor(listCursor{
			ID: rows[len(rows)-1].ID, Key: listSortKey(rows[len(rows)-1], field), Field: field, Desc: dir < 0,
		})
		out.NextCursor = &token
	}
	for i := range rows {
		out.Data = append(out.Data, ViewOf(&rows[i]))
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := r.coll().CountDocuments(ctx, base, options.Count().SetLimit(totalListCap+1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "submit.list")
		}
		capped := n > totalListCap
		if capped {
			n = totalListCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

// listMatch builds the filters of GET /v1/outbound-messages (S2 §4.3).
func listMatch(f ListFilter) bson.M {
	filter := bson.M{}
	if f.AccountID != "" {
		filter["accountId"] = f.AccountID
	} else if len(f.AccountIDs) > 0 {
		filter["accountId"] = bson.M{"$in": f.AccountIDs}
	}
	if f.State != "" {
		filter["state"] = f.State
	}
	if f.MessageID != "" {
		filter["messageIdHeader"] = f.MessageID
	}
	if f.From != "" {
		filter["envelope.from"] = strings.ToLower(f.From)
	}
	if f.To != "" {
		filter["envelope.to"] = f.To
	}
	if f.CreatedFrom != nil || f.CreatedTo != nil {
		rng := bson.M{}
		if f.CreatedFrom != nil {
			rng["$gte"] = f.CreatedFrom.UTC()
		}
		if f.CreatedTo != nil {
			rng["$lt"] = f.CreatedTo.UTC()
		}
		filter["createdAt"] = rng
	}
	return filter
}

func listLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultListLimit
	case limit > maxListLimit:
		return maxListLimit
	default:
		return limit
	}
}

func listSort(value string) (string, int, error) {
	if value == "" {
		value = "-id"
	}
	desc := strings.HasPrefix(value, "-")
	name := strings.TrimPrefix(value, "-")
	switch name {
	case "id":
		name = "_id"
	case "createdAt", "sendAt", "state":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", value)
	}
	if desc {
		return name, -1, nil
	}
	return name, 1, nil
}

func listSortKey(m Message, field string) any {
	switch field {
	case "state":
		return m.State
	case "createdAt":
		return m.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "sendAt":
		if m.SendAt == nil {
			return nil
		}
		return m.SendAt.UTC().Format(time.RFC3339Nano)
	default:
		return m.ID
	}
}

// listQuery adds the keyset or page window to the filter.
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
	if field == "createdAt" || field == "sendAt" {
		text, ok := key.(string)
		if !ok {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		if key, err = time.Parse(time.RFC3339Nano, text); err != nil {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
	}
	window := bson.M{"$or": []bson.M{
		{field: bson.M{cmp: key}},
		{field: key, "_id": bson.M{cmp: decoded.ID}},
	}}
	return bson.M{"$and": []bson.M{base, window}}, opts, nil
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
		return listCursor{}, errors.New("submit: empty cursor")
	}
	return cursor, nil
}

// formatETag and checkIfMatch implement the optimistic concurrency of S2 §2.
func formatETag(version int64) string { return fmt.Sprintf(`"v%d"`, version) }

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch == "" {
		return nil
	}
	value := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ifMatch), `"`), "v")
	got, err := strconv.ParseInt(value, 10, 64)
	if err != nil || got != version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

// validState reports whether value is one of the S6 §1 states (list filter).
func validState(value string) bool {
	return slices.Contains([]string{StateScheduled, StateQueued, StateSending, StateSent, StateFailed,
		StateOutcomeUnknown, StateCancelled, StateCaptured}, value)
}
