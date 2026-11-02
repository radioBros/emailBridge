package audit

import (
	"context"
	"testing"
)

func TestDiffStructFieldsFallbackAndEmpty(t *testing.T) {
	type noJSON struct {
		Alpha string
	}
	ch := Diff(noJSON{Alpha: "1"}, noJSON{Alpha: "2"})
	if ch["Alpha"].Before != "1" || ch["Alpha"].After != "2" {
		t.Fatalf("%+v", ch)
	}
	if Diff(nil, nil) != nil {
		t.Fatal("nil")
	}
	if Diff(map[string]any{"a": ""}, map[string]any{}) != nil {
		t.Fatal("empty skip")
	}
	p := "x"
	if Diff(&p, nil) == nil {
		t.Fatal("pointer unwrap")
	}
	// Changes type via toMap
	_ = Diff(Changes{"a": {Before: 1}}, Changes{"a": {Before: 2}})
}

func TestParseSortAndCursorErrors(t *testing.T) {
	if _, _, err := parseSort("nope"); err == nil {
		t.Fatal("bad sort")
	}
	f, d, err := parseSort("id")
	if err != nil || f != "_id" || d != 1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("bad cursor")
	}
	if _, err := decodeCursor(encodeCursor(cursorPayload{})); err == nil {
		t.Fatal("empty id cursor")
	}
	_, err = cursorFilter(cursorPayload{ID: "aud_x", At: "not-time", ByAt: true}, "at", -1)
	if err == nil {
		t.Fatal("bad at in cursor filter")
	}
}

func TestWriterNil(t *testing.T) {
	var w *Writer
	if err := w.Record(context.Background(), "x", Target{Type: "t", ID: "1"}, nil); err == nil {
		t.Fatal("nil writer")
	}
}

func TestStructFieldsFallbackAndRoles(t *testing.T) {
	// json.Marshal fails on func fields → structFields path
	type bad struct {
		Name string
		Skip func()
	}
	ch := Diff(bad{Name: "a"}, bad{Name: "b"})
	if ch["Name"].Before != "a" {
		t.Fatalf("%+v", ch)
	}
	m := &Module{}
	if len(m.Roles()) != 1 {
		t.Fatal("roles")
	}
	if redactStored("password", Secret("x")) != redacted {
		t.Fatal("redactStored Secret")
	}
	if !isEmpty((*string)(nil)) || !isEmpty("") {
		t.Fatal("isEmpty")
	}
	_, err := cursorFilter(cursorPayload{ID: "aud_x"}, "_id", 1)
	if err != nil {
		t.Fatal(err)
	}
}
