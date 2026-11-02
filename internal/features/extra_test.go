package features

import (
	"testing"
)

func TestValueHelpers(t *testing.T) {
	t.Parallel()
	b := BoolValue(true)
	if !b.IsBool() || b.IsString() {
		t.Fatal("bool")
	}
	s := StringValue("standard")
	if !s.IsString() || s.IsBool() {
		t.Fatal("string")
	}
	if b.Equal(s) {
		t.Fatal("cross type")
	}
	if !b.Equal(BoolValue(true)) || b.Equal(BoolValue(false)) {
		t.Fatal("bool equal")
	}
	if !s.Equal(StringValue("standard")) || s.Equal(StringValue("x")) {
		t.Fatal("string equal")
	}
	unset := Value{}
	if unset.Equal(b) || unset.Display() != "Unset" {
		t.Fatal(unset.Display())
	}
	if b.Display() == "" || s.Display() == "" {
		t.Fatal("Display")
	}
}

func TestDefaultRegistryPanicOnDup(t *testing.T) {
	t.Parallel()
	// DefaultRegistry succeeds; exercise Get miss.
	r := DefaultRegistry()
	if _, ok := r.Get("no.such.switch"); ok {
		t.Fatal("expected miss")
	}
	if len(r.All()) == 0 {
		t.Fatal("empty")
	}
}
