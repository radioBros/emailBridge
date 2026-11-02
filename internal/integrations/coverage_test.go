package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestCursorHelpersAndSortKey(t *testing.T) {
	inst := Instance{ID: "int_a", Name: "n", CreatedAt: time.Unix(1, 0).UTC()}
	if sortKey(inst, "name") != "n" {
		t.Fatal("name")
	}
	if sortKey(inst, "createdAt") == "" {
		t.Fatal("createdAt")
	}
	if sortKey(inst, "_id") != "int_a" {
		t.Fatal("id")
	}
	tok := encodeCursor(listCursor{ID: "int_a", Key: "n", Field: "name", Desc: true})
	c, err := decodeCursor(tok)
	if err != nil || c.ID != "int_a" || c.Field != "name" || !c.Desc {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := decodeCursor("%%%"); err == nil {
		t.Fatal("bad cursor")
	}
	kf, err := cursorFilter(listCursor{ID: "int_b", Field: "_id"}, "_id", 1)
	if err != nil || kf["_id"] == nil {
		t.Fatal(kf, err)
	}
	kf, err = cursorFilter(listCursor{ID: "int_b", Key: "n", Field: "name"}, "name", -1)
	if err != nil {
		t.Fatal(err)
	}
	_ = kf
	if _, err := cursorFilter(listCursor{ID: "x", Field: "name"}, "_id", 1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	field, dir, err := parseSort("-name")
	if err != nil || field != "name" || dir != -1 {
		t.Fatal(field, dir, err)
	}
	if _, _, err := parseSort(""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseSort("id"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckValueTypes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flag": map[string]any{"type": "boolean"},
			"n":    map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
			"s":    map[string]any{"type": "string", "minLength": 2, "maxLength": 5},
		},
	}
	if err := validateConfig(schema, map[string]any{"flag": true, "n": float64(5), "s": "ab"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"flag": "x"}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"n": float64(0)}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"n": float64(11)}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"n": "x"}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"s": "a"}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"s": "abcdef"}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"s": 1}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if f, ok := asFloatOK(float64(3)); !ok || f != 3 {
		t.Fatal(f, ok)
	}
	if _, ok := asFloatOK(nil); ok {
		t.Fatal("nil")
	}
	if asFloat(int(2)) != 2 || asFloat(int32(2)) != 2 || asFloat(int64(2)) != 2 {
		t.Fatal("asFloat")
	}
	if err := validateConfig(map[string]any{
		"properties": map[string]any{"x": map[string]any{"type": "array"}},
	}, map[string]any{"x": []any{}}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestModuleMetaAndRegistryEmpty(t *testing.T) {
	m := &Module{}
	if m.Name() != "integrations" {
		t.Fatal(m.Name())
	}
	// nil Roles = all roles (API + scheduler digest duty), same as exports.
	if m.Roles() != nil {
		t.Fatalf("want nil roles, got %v", m.Roles())
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("nil def")
	}
	if err := r.Register(&Definition{}); err == nil {
		t.Fatal("empty id")
	}
}

func TestRunTestNoHook(t *testing.T) {
	svc := &Service{}
	tr, err := svc.runTest(context.Background(), &Definition{ID: "x"}, nil, nil)
	if err != nil || !tr.OK {
		t.Fatal(tr, err)
	}
	if svc.client() == nil || svc.reg() == nil {
		t.Fatal("defaults")
	}
}

func TestValidateRequiredSecret(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"token"},
		"properties": map[string]any{
			"token": map[string]any{"type": "string", "writeOnly": true},
		},
	}
	if err := validateConfig(schema, map[string]any{}, nil, []string{"token"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateConfig(schema, map[string]any{"token": "x"}, map[string]string{}, []string{"token"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("secret in config")
	}
}

func TestApplySchemaDefaultsNilProps(t *testing.T) {
	applySchemaDefaults(&Definition{ConfigSchema: map[string]any{}}, map[string]any{})
	applySchemaDefaults(&Definition{ConfigSchema: map[string]any{
		"properties": map[string]any{"a": "bad"},
	}}, map[string]any{})
	cfg := cloneMap(nil)
	if len(cfg) != 0 {
		t.Fatal(cfg)
	}
	_ = cloneStrMap(nil)
}
