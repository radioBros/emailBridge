package templates

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestCursorFilterAndSortKeyCreatedAt(t *testing.T) {
	cur := cursorPayload{ID: "tpl_1", Field: "name", Key: "mid", Desc: true}
	f, err := cursorFilter(cur, "name", -1)
	if err != nil {
		t.Fatal(err)
	}
	or, ok := f["$or"].([]bson.M)
	if !ok || len(or) != 2 {
		t.Fatalf("%v", f)
	}
	f2, err := cursorFilter(cursorPayload{ID: "tpl_1", Field: "_id"}, "_id", 1)
	if err != nil || f2["_id"] == nil {
		t.Fatal(err)
	}
	if _, err := cursorFilter(cursorPayload{ID: "tpl_1", Field: "name"}, "name", 1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("nil key")
	}
	tpl := Template{ID: "tpl_1", CreatedAt: time.Unix(10, 0).UTC()}
	if sortKey(tpl, "createdAt") != tpl.CreatedAt.Format(time.RFC3339Nano) {
		t.Fatal("createdAt key")
	}
	if _, err := decodeCursor(encodeCursor(cursorPayload{Field: "name"})); err == nil {
		t.Fatal("incomplete cursor")
	}
	f3, d, err := parseSort("")
	if err != nil || f3 != "_id" || d != -1 {
		t.Fatalf("default sort %s %d %v", f3, d, err)
	}
}
