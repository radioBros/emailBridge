package exports

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

type cursorPayload struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func parseSort(s string) (field string, dir int, err error) {
	if s == "" {
		return "createdAt", -1, nil
	}
	desc := false
	if s[0] == '-' {
		desc = true
		s = s[1:]
	}
	switch s {
	case "createdAt", "id":
	default:
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", s)
	}
	field = s
	if field == "id" {
		field = "_id"
	}
	dir = 1
	if desc {
		dir = -1
	}
	return field, dir, nil
}

func sortKey(e Export, field string) any {
	switch field {
	case "createdAt":
		return e.CreatedAt.UTC().Format(time.RFC3339Nano)
	default:
		return e.ID
	}
}

func encodeCursor(p cursorPayload) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorPayload, error) {
	var p cursorPayload
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	return p, err
}

func cursorFilter(cur cursorPayload, sortField string, sortDir int) (bson.M, error) {
	op := "$gt"
	if sortDir < 0 {
		op = "$lt"
	}
	if sortField == "_id" {
		return bson.M{"_id": bson.M{op: cur.ID}}, nil
	}
	key := cur.Key
	if sortField == "createdAt" {
		if ks, ok := cur.Key.(string); ok {
			t, err := time.Parse(time.RFC3339Nano, ks)
			if err != nil {
				return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
			}
			key = t
		}
	}
	return bson.M{"$or": []bson.M{
		{sortField: bson.M{op: key}},
		{sortField: key, "_id": bson.M{op: cur.ID}},
	}}, nil
}
