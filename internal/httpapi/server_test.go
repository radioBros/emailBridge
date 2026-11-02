package httpapi

import (
	"reflect"
	"testing"
)

func TestUniqueSchemaNamer(t *testing.T) {
	n := uniqueSchemaNamer()
	plain := reflect.TypeOf(Options{})
	first, again := n(plain, ""), n(plain, "")
	if first != "Options" || again != "Options" {
		t.Fatal("the first owner keeps the plain name, repeatedly")
	}
	type Options struct{ B string } // same name, different type (stands in for another package's type)
	if got := n(reflect.TypeOf(Options{}), ""); got != "HttpapiOptions" {
		t.Fatalf("qualified: %s", got)
	}
	if got := n(reflect.TypeOf(&Options{}), ""); got != "HttpapiOptions" {
		t.Fatalf("pointer: %s", got)
	}
	if n(reflect.TypeOf([]int{}), "Hint") == "" {
		t.Fatal("anonymous types use the hint")
	}
}
