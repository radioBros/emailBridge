package suppression

import (
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	tok, err := encodeCursor(listCursor{ID: "sup_1", Key: "name", Field: "name", Desc: true})
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	c, err := decodeCursor(tok)
	if err != nil || c.ID != "sup_1" || c.Field != "name" || !c.Desc {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("bad")
	}
	if _, err := decodeCursor(""); err == nil {
		t.Fatal("empty")
	}
}
