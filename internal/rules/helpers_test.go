package rules

import (
	"testing"

	"cel.dev/cel-go/common/types"
)

func TestHelpersDomainAddresses(t *testing.T) {
	if got := domainFn(types.String("a@Example.COM")); string(got.(types.String)) != "example.com" {
		t.Fatal(got)
	}
	if got := domainFn(types.DefaultTypeAdapter.NativeToValue(map[string]any{"address": "x@Y.Z"})); string(got.(types.String)) != "y.z" {
		t.Fatal(got)
	}
	if got := domainFn(types.String("nodomain")); string(got.(types.String)) != "" {
		t.Fatal(got)
	}
	list := types.DefaultTypeAdapter.NativeToValue([]any{
		map[string]any{"address": "a@b.com"},
		"c@d.com",
	})
	got := addressesFn(list)
	native := got.Value().([]string)
	if len(native) != 2 || native[0] != "a@b.com" || native[1] != "c@d.com" {
		t.Fatalf("%v", native)
	}
}

func TestSetEvalHeaders(t *testing.T) {
	setEvalHeaders(map[string]any{
		"headers": map[string]any{
			"X-Foo": []any{"a", "b"},
			"Bar":   []string{"c"},
		},
	})
	h := getHeaders()
	if h["x-foo"][0] != "a" || h["bar"][0] != "c" {
		t.Fatalf("%v", h)
	}
	setEvalHeaders(nil)
	if len(getHeaders()) != 0 {
		t.Fatal(getHeaders())
	}
}

func TestRuntimeCostEstimator(t *testing.T) {
	var e runtimeCostEstimator
	if e.CallCost("header", "", nil, nil) == nil {
		t.Fatal("header")
	}
	if e.CallCost("headerValues", "", nil, nil) == nil {
		t.Fatal("headerValues")
	}
	if e.CallCost("unknown", "", nil, nil) != nil {
		t.Fatal("unknown")
	}
}
