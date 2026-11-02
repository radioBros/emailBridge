package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

// fieldPath returns the single problem item path of a metadata validation error ("" when there is none).
func fieldPath(t *testing.T, err error) string {
	t.Helper()
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("want validation_failed, got %v", err)
	}
	items := fieldErrorsOf(err)
	if len(items) != 1 {
		t.Fatalf("want one field error, got %+v", items)
	}
	return items[0].Path
}

func TestNormalizeMetadataAcceptsScalars(t *testing.T) {
	got, err := normalizeMetadata(map[string]any{
		"crm.id": "48213", "tier": 2, "score": 1.5, "big": int64(7), "vip": true, "gone": nil,
		"Tenant_Code-1": strings.Repeat("x", MetadataMaxValueBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["tier"] != float64(2) || got["big"] != float64(7) || got["score"] != 1.5 {
		t.Fatalf("numbers are stored as float64: %#v", got)
	}
	if v, ok := got["gone"]; !ok || v != nil {
		t.Fatalf("null is a stored value on create: %#v", got)
	}
	if got["crm.id"] != "48213" || got["vip"] != true {
		t.Fatalf("%#v", got)
	}
	empty, err := normalizeMetadata(map[string]any{})
	if err != nil || empty != nil {
		t.Fatalf("empty object = %v, %v; want nil", empty, err)
	}
}

func TestNormalizeMetadataRejects(t *testing.T) {
	many := map[string]any{}
	for i := 0; i <= MetadataMaxKeys; i++ {
		many[strings.Repeat("k", i+1)] = "v"
	}
	// 31 values of 270 bytes ≈ 8.5 KB serialised, still within the key count.
	large := map[string]any{}
	for i := 0; i < MetadataMaxKeys-1; i++ {
		large[string(rune('a'+i%26))+strings.Repeat("z", i/26+1)] = strings.Repeat("v", 270)
	}
	cases := []struct {
		name string
		in   map[string]any
		path string
	}{
		{"too many keys", many, "body.metadata"},
		{"too large", large, "body.metadata"},
		{"bad key char", map[string]any{"a b": "x"}, "body.metadata.a b"},
		{"dollar key", map[string]any{"$where": "x"}, "body.metadata.$where"},
		{"key too long", map[string]any{strings.Repeat("k", 65): "x"}, "body.metadata." + strings.Repeat("k", 65)},
		{"value too long", map[string]any{"note": strings.Repeat("é", 513)}, "body.metadata.note"},
		{"nested object", map[string]any{"obj": map[string]any{"a": 1}}, "body.metadata.obj"},
		{"array", map[string]any{"tags": []any{"a"}}, "body.metadata.tags"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := normalizeMetadata(c.in)
			if got := fieldPath(t, err); got != c.path {
				t.Fatalf("path = %q, want %q", got, c.path)
			}
		})
	}
}

func TestDecodeMetadataPatch(t *testing.T) {
	p, clear, err := decodeMetadataPatch(json.RawMessage(` null `))
	if err != nil || !clear || p != nil {
		t.Fatalf("null: %v %v %v", p, clear, err)
	}
	p, clear, err = decodeMetadataPatch(json.RawMessage(`{"tier":3,"old":null}`))
	if err != nil || clear {
		t.Fatal(err)
	}
	if p["tier"] != float64(3) {
		t.Fatalf("numbers decode as float64 (never json.Number): %#v", p["tier"])
	}
	if v, ok := p["old"]; !ok || v != nil {
		t.Fatalf("null member kept as a removal: %#v", p)
	}
	p, _, err = decodeMetadataPatch(json.RawMessage(`{}`))
	if err != nil || p == nil || len(p) != 0 {
		t.Fatalf("empty object: %#v %v", p, err)
	}
	for _, bad := range []string{`"x"`, `[1]`, `3`, `true`} {
		if _, _, err := decodeMetadataPatch(json.RawMessage(bad)); fieldPath(t, err) != "body.metadata" {
			t.Fatalf("%s: %v", bad, err)
		}
	}
}

func TestMergeMetadata(t *testing.T) {
	cur := map[string]any{"crm": "a", "tier": float64(1), "old": "x"}
	got, err := mergeMetadata(cur, map[string]any{"tier": float64(2), "old": nil, "new": true, "missing": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"crm": "a", "tier": float64(2), "new": true}
	if !metadataEqual(got, want) {
		t.Fatalf("merged = %#v, want %#v", got, want)
	}
	if cur["tier"] != float64(1) || cur["old"] != "x" {
		t.Fatalf("cur must not be modified: %#v", cur)
	}
	all, err := mergeMetadata(cur, map[string]any{"crm": nil, "tier": nil, "old": nil})
	if err != nil || all != nil {
		t.Fatalf("removing every key = %#v, %v; want nil", all, err)
	}
	// The merged result is re-validated: 32 stored keys + 1 new one is too many.
	full := map[string]any{}
	for i := 0; i < MetadataMaxKeys; i++ {
		full[strings.Repeat("k", i+1)] = "v"
	}
	if _, err := mergeMetadata(full, map[string]any{"extra": "v"}); fieldPath(t, err) != "body.metadata" {
		t.Fatalf("want too many keys, got %v", err)
	}
	if _, err := mergeMetadata(full, map[string]any{"k": nil, "extra": "v"}); err != nil {
		t.Fatalf("replace one key keeps the count: %v", err)
	}
	if _, err := mergeMetadata(cur, map[string]any{"bad key": nil}); fieldPath(t, err) != "body.metadata.bad key" {
		t.Fatalf("invalid key even when removing: %v", err)
	}
	if _, err := mergeMetadata(cur, map[string]any{"obj": map[string]any{}}); fieldPath(t, err) != "body.metadata.obj" {
		t.Fatalf("nested value: %v", err)
	}
}

func TestMetadataListFilter(t *testing.T) {
	if f, err := metadataListFilter(nil); err != nil || f != nil {
		t.Fatalf("none: %v %v", f, err)
	}
	one, err := metadataListFilter(map[string]string{"crm.id": "48213"})
	if err != nil {
		t.Fatal(err)
	}
	want := bson.M{"$expr": bson.M{"$eq": bson.A{
		bson.M{"$getField": bson.M{"field": bson.M{"$literal": "crm.id"}, "input": "$metadata"}},
		bson.M{"$literal": "48213"},
	}}}
	if !reflect.DeepEqual(one, want) {
		t.Fatalf("filter\n got %#v\nwant %#v", one, want)
	}
	for k := range one {
		if strings.Contains(k, "crm") {
			t.Fatalf("the key must never become a field path: %#v", one)
		}
	}
	three, err := metadataListFilter(map[string]string{"a": "1", "b": "2", "c": "3"})
	if err != nil {
		t.Fatal(err)
	}
	and, _ := three["$expr"].(bson.M)["$and"].([]any)
	if len(and) != 3 {
		t.Fatalf("three filters are and-ed: %#v", three)
	}
	if _, err := metadataListFilter(map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}); fieldPath(t, err) != "query.metadata" {
		t.Fatalf("four filters: %v", err)
	}
	if _, err := metadataListFilter(map[string]string{"$ne": "1"}); fieldPath(t, err) != "query.metadata.$ne" {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := metadataListFilter(map[string]string{"a": strings.Repeat("v", MetadataMaxValueBytes+1)}); fieldPath(t, err) != "query.metadata.a" {
		t.Fatalf("long value: %v", err)
	}
}

func TestWithFieldErrorsBuildsProblemItems(t *testing.T) {
	_, err := normalizeMetadata(map[string]any{"a b": "x"})
	out := withFieldErrors(context.Background(), err)
	var p *errs.Problem
	if !errors.As(out, &p) {
		t.Fatalf("want *errs.Problem, got %T", out)
	}
	if p.Status != http.StatusUnprocessableEntity || p.Code != errs.CodeValidationFailed {
		t.Fatalf("%+v", p)
	}
	if len(p.Errors) != 1 || p.Errors[0].Path != "body.metadata.a b" || p.Errors[0].Code != "invalid_key" {
		t.Fatalf("errors = %+v", p.Errors)
	}
	// Errors without field items stay as they are (httpapi renders them).
	plain := errs.New(errs.CodeNotFound, "nope")
	if out := withFieldErrors(context.Background(), plain); !errors.Is(out, plain) || errs.CodeOf(out) != errs.CodeNotFound {
		t.Fatal("plain error changed")
	}
}

func TestListInResolveCollectsMetadataFilters(t *testing.T) {
	ctx := func(rawQuery string) huma.Context {
		r := httptest.NewRequest(http.MethodGet, "/v1/accounts?"+rawQuery, nil)
		return humatest.NewContext(&huma.Operation{}, r, httptest.NewRecorder())
	}
	var in listIn
	if problems := in.Resolve(ctx("limit=5&metadata.crm.id=48213&metadata.tier=gold&q=x")); len(problems) != 0 {
		t.Fatalf("%v", problems)
	}
	if len(in.metadata) != 2 || in.metadata["crm.id"] != "48213" || in.metadata["tier"] != "gold" {
		t.Fatalf("metadata = %#v", in.metadata)
	}
	var dup listIn
	problems := dup.Resolve(ctx("metadata.crm=a&metadata.crm=b"))
	if len(problems) != 1 {
		t.Fatalf("duplicate key: %v", problems)
	}
	var d *huma.ErrorDetail
	if !errors.As(problems[0], &d) || d.Location != "query.metadata.crm" {
		t.Fatalf("detail = %#v", problems[0])
	}
	var none listIn
	if problems := none.Resolve(ctx("state=connected")); len(problems) != 0 || none.metadata != nil {
		t.Fatalf("no metadata filters: %v %v", problems, none.metadata)
	}
}

func TestPatchBodyCarriesMetadata(t *testing.T) {
	var b patchBody
	if err := json.Unmarshal([]byte(`{"metadata":{"a":1}}`), &b); err != nil {
		t.Fatal(err)
	}
	if string(b.Metadata) != `{"a":1}` {
		t.Fatalf("%q", b.Metadata)
	}
	var clear patchBody
	if err := json.Unmarshal([]byte(`{"metadata":null}`), &clear); err != nil {
		t.Fatal(err)
	}
	if string(clear.Metadata) != "null" {
		t.Fatalf("%q", clear.Metadata)
	}
}

func TestViewCopiesMetadata(t *testing.T) {
	a := Account{Metadata: map[string]any{"crm": "a"}}
	v := a.View()
	v.Metadata["crm"] = "changed"
	if a.Metadata["crm"] != "a" {
		t.Fatal("View must not share the stored map")
	}
	if (Account{}).View().Metadata != nil {
		t.Fatal("no metadata → omitted")
	}
}
