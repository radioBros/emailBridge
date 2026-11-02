package rules

import (
	"testing"
	"time"
)

func TestParseSort(t *testing.T) {
	f, d, err := parseSort("")
	if err != nil || f != "_id" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	f, d, err = parseSort("name")
	if err != nil || f != "name" || d != 1 {
		t.Fatal(f, d, err)
	}
	if _, _, err := parseSort("nope"); err == nil {
		t.Fatal("expected sort error")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	p := cursorPayload{ID: "rte_x", Field: "_id", Desc: true}
	s := encodeCursor(p)
	got, err := decodeCursor(s)
	if err != nil || got.ID != p.ID {
		t.Fatal(got, err)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("bad cursor")
	}
	kf, err := cursorFilter(cursorPayload{ID: "rte_a"}, "_id", -1)
	if err != nil || kf["_id"] == nil {
		t.Fatal(kf, err)
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	kf, err = cursorFilter(cursorPayload{ID: "rte_a", Key: at, Field: "createdAt"}, "createdAt", -1)
	if err != nil {
		t.Fatal(err)
	}
	_ = kf
}

func TestSortKey(t *testing.T) {
	r := Route{ID: "rte_1", Name: "n", Order: 3, CreatedAt: time.Unix(1, 0).UTC()}
	if sortKey(r, "name") != "n" || sortKey(r, "order") != 3 {
		t.Fatal(sortKey(r, "name"))
	}
	_ = sortKey(r, "createdAt")
	_ = sortKey(r, "_id")
}

func TestParseFormatETag(t *testing.T) {
	if formatETag(3) != `"v3"` {
		t.Fatal(formatETag(3))
	}
	if parseETag(`"v3"`) != 3 || parseETag("v3") != 3 || parseETag("3") != 3 {
		t.Fatal(parseETag(`"v3"`))
	}
}
