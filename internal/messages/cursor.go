package messages

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

type cursorPayload struct {
	ID    string `json:"id"`
	Field string `json:"field,omitempty"`
	Desc  bool   `json:"desc,omitempty"`
	Key   any    `json:"key,omitempty"`
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

func parseMsgSort(s string) (field string, dir int, err error) {
	if s == "" {
		s = "-internalDate"
	}
	desc := len(s) > 0 && s[0] == '-'
	name := s
	if desc {
		name = s[1:]
	}
	switch name {
	case "internalDate", "size", "uid", "id":
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

func msgSortKey(m *msgDoc, field string) any {
	switch field {
	case "internalDate":
		return m.InternalDate.UTC().Format(time.RFC3339Nano)
	case "size":
		return m.Size
	case "uid":
		return m.UID
	default:
		return m.ID
	}
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
	if sortField == "internalDate" {
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
