package rules

import (
	"context"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// G4: account.metadata is available to filters and maps, always as a map.
func TestAccountMetadataInFilterAndMap(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(
		`account.metadata.crm == "x" && account.metadata["crm.id"] == "48213" && account.metadata.tier >= 2`,
		`{"crm": account.metadata.crm, "vip": account.metadata.vip, "all": account.metadata}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	event := map[string]any{"type": "message.received", "data": map[string]any{}}
	meta := map[string]any{"crm": "x", "crm.id": "48213", "tier": float64(2), "vip": true}
	account := map[string]any{"id": "acc_1", "metadata": meta}

	matched, mapped, _, err := e.EvalWithLists(context.Background(), p, event, account, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("filter on account.metadata should match")
	}
	want := map[string]any{"crm": "x", "vip": true, "all": map[string]any{"crm": "x", "crm.id": "48213", "tier": float64(2), "vip": true}}
	if !reflect.DeepEqual(mapped, want) {
		t.Fatalf("mapped = %#v, want %#v", mapped, want)
	}

	other := map[string]any{"id": "acc_2", "metadata": map[string]any{"crm": "y", "crm.id": "48213", "tier": float64(2)}}
	if matched, _, _, err := e.EvalWithLists(context.Background(), p, event, other, nil, nil); err != nil || matched {
		t.Fatalf("other crm: matched=%v err=%v", matched, err)
	}
}

// Accounts without metadata still get an (empty) map, so guarded expressions work everywhere.
func TestAccountMetadataGuardsWithoutMetadata(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(`!has(account.metadata.crm) && !("crm" in account.metadata) && size(account.metadata) == 0`, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range []map[string]any{nil, {"id": "acc_1"}, {"id": "acc_1", "metadata": nil}, {"metadata": "not a map"}} {
		matched, _, _, err := e.EvalWithLists(context.Background(), p, map[string]any{"type": "x"}, acc, nil, nil)
		if err != nil || !matched {
			t.Fatalf("account %#v: matched=%v err=%v", acc, matched, err)
		}
	}
}

func TestNormalizeAccountMetadata(t *testing.T) {
	src := bson.M{"crm": "x"}
	got := normalizeAccount(map[string]any{"metadata": src}, map[string]any{}, map[string]any{})
	m, ok := got["metadata"].(map[string]any)
	if !ok || m["crm"] != "x" {
		t.Fatalf("bson.M metadata = %#v", got["metadata"])
	}
	m["crm"] = "changed"
	if src["crm"] != "x" {
		t.Fatal("normalizeAccount must copy the metadata map")
	}
	got = normalizeAccount(nil, map[string]any{}, map[string]any{})
	if m, ok := got["metadata"].(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("missing metadata = %#v", got["metadata"])
	}
}
