//go:build integration

package accounts

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
)

// G4: metadata round-trips through MongoDB (dotted keys, numbers as JSON numbers, null values on create).
func TestIntegrationMetadataCreateGetPatch(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	in := sampleCreate("meta-1")
	in.Metadata = map[string]any{"crm.id": "48213", "tier": float64(2), "vip": true, "legacy": nil}
	a, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got.View())
	var view struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if view.Metadata["crm.id"] != "48213" || view.Metadata["tier"] != float64(2) || view.Metadata["vip"] != true {
		t.Fatalf("metadata = %s", b)
	}
	if v, ok := view.Metadata["legacy"]; !ok || v != nil {
		t.Fatalf("null stored on create: %s", b)
	}
	if !strings.Contains(string(b), `"tier":2`) {
		t.Fatalf("number must read back as a JSON number: %s", b)
	}
	// The dotted key is one literal key in MongoDB, not a nested path.
	var stored struct {
		Metadata map[string]any `bson:"metadata"`
	}
	if err := svc.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": a.ID}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.Metadata["crm.id"] != "48213" || stored.Metadata["crm"] != nil {
		t.Fatalf("stored metadata = %#v", stored.Metadata)
	}

	// Merge patch: change one key, remove two, add one; the rest stays.
	p, err := svc.Patch(ctx, a.ID, PatchInput{Metadata: map[string]any{
		"tier": float64(3), "legacy": nil, "vip": nil, "region.code": "IT-MI",
	}}, `"v1"`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"crm.id": "48213", "tier": float64(3), "region.code": "IT-MI"}
	if !metadataEqual(p.Metadata, want) {
		t.Fatalf("patched = %#v", p.Metadata)
	}
	reloaded, _ := svc.load(ctx, a.ID)
	if !metadataEqual(reloaded.Metadata, want) || reloaded.Version != 2 {
		t.Fatalf("stored = %#v v%d", reloaded.Metadata, reloaded.Version)
	}

	// account.updated lists metadata; the audit record carries the before/after diff.
	var ev struct {
		Data string `bson:"data"`
	}
	if err := svc.DB.Collection(events.Collection).FindOne(ctx, bson.M{"accountId": a.ID, "type": events.AccountUpdated}).Decode(&ev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.Data, `"metadata"`) {
		t.Fatalf("account.updated changed: %s", ev.Data)
	}
	var rec audit.Record
	if err := svc.DB.Collection(audit.Collection).FindOne(ctx, bson.M{"target.id": a.ID, "action": "account.update"}).Decode(&rec); err != nil {
		t.Fatal(err)
	}
	fc, ok := rec.Changes["metadata"]
	if !ok {
		t.Fatalf("audit changes = %#v", rec.Changes)
	}
	before, _ := json.Marshal(fc.Before)
	after, _ := json.Marshal(fc.After)
	if !strings.Contains(string(before), `"vip":true`) || strings.Contains(string(after), "vip") || !strings.Contains(string(after), "IT-MI") {
		t.Fatalf("audit diff before=%s after=%s", before, after)
	}

	// A patch that changes nothing writes nothing.
	same, err := svc.Patch(ctx, a.ID, PatchInput{Metadata: map[string]any{"tier": float64(3)}}, "")
	if err != nil || same.Version != 2 {
		t.Fatalf("no-op patch: v%d %v", same.Version, err)
	}

	// The merged result is re-validated.
	if _, err := svc.Patch(ctx, a.ID, PatchInput{Metadata: map[string]any{"bad key": "x"}}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("invalid key: %v", err)
	}
	big := map[string]any{}
	for i := 0; i < MetadataMaxKeys; i++ {
		big["k"+strings.Repeat("x", i)] = "v"
	}
	if _, err := svc.Patch(ctx, a.ID, PatchInput{Metadata: big}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("33 keys after merge: %v", err)
	}

	// metadata: null removes every key ($unset).
	cleared, err := svc.Patch(ctx, a.ID, PatchInput{ClearMetadata: true}, "")
	if err != nil || cleared.Metadata != nil {
		t.Fatalf("clear: %#v %v", cleared.Metadata, err)
	}
	raw := bson.M{}
	_ = svc.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": a.ID}).Decode(&raw)
	if _, ok := raw["metadata"]; ok {
		t.Fatalf("metadata should be unset: %#v", raw["metadata"])
	}
}

func TestIntegrationMetadataCreateRejected(t *testing.T) {
	svc, ctx, org := testSvc(t)
	in := sampleCreate("meta-bad")
	in.Metadata = map[string]any{"nested": map[string]any{"a": 1}}
	_, err := svc.Create(ctx, in)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("want validation_failed, got %v", err)
	}
	if items := fieldErrorsOf(err); len(items) != 1 || items[0].Path != "body.metadata.nested" {
		t.Fatalf("field errors = %+v", items)
	}
	if n, _ := svc.DB.Collection(Collection).CountDocuments(ctx, bson.M{"organizationId": org}); n != 0 {
		t.Fatalf("nothing may be stored, found %d", n)
	}
}

func TestIntegrationMetadataListFilter(t *testing.T) {
	svc, ctx, org := testSvc(t)
	mk := func(ext string, meta map[string]any) string {
		in := sampleCreate(ext)
		in.Metadata = meta
		a, err := svc.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	a1 := mk("m1", map[string]any{"crm.id": "1", "tier": "gold"})
	a2 := mk("m2", map[string]any{"crm.id": "2", "tier": "gold"})
	a3 := mk("m3", map[string]any{"tier": float64(1)}) // tier is a number here
	_ = mk("m4", nil)
	// A nested document {crm: {id: "1"}} must not match the literal key "crm.id".
	if _, err := svc.coll().InsertOne(ctx, bson.M{
		"_id": "acc_nested", "organizationId": org, "name": "n", "email": "n@example.com",
		"state": StateNew, "metadata": bson.M{"crm": bson.M{"id": "1"}}, "version": 1,
	}); err != nil {
		t.Fatal(err)
	}

	ids := func(f ListFilter) []string {
		t.Helper()
		res, err := svc.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, a := range res.Data {
			out = append(out, a.ID)
		}
		return out
	}
	if got := ids(ListFilter{Metadata: map[string]string{"crm.id": "1"}}); len(got) != 1 || got[0] != a1 {
		t.Fatalf("crm.id=1 → %v, want [%s]", got, a1)
	}
	got := ids(ListFilter{Metadata: map[string]string{"tier": "gold"}, Sort: "createdAt"})
	if len(got) != 2 || got[0] != a1 || got[1] != a2 {
		t.Fatalf("tier=gold → %v", got)
	}
	if got := ids(ListFilter{Metadata: map[string]string{"tier": "gold", "crm.id": "2"}}); len(got) != 1 || got[0] != a2 {
		t.Fatalf("and → %v", got)
	}
	if got := ids(ListFilter{Metadata: map[string]string{"tier": "1"}}); len(got) != 0 {
		t.Fatalf("string equality never matches a number: %v (a3=%s)", got, a3)
	}
	// Works with includeTotal and with cursor paging.
	res, err := svc.List(ctx, ListFilter{Metadata: map[string]string{"tier": "gold"}, Page: 1, Limit: 1, IncludeTotal: true})
	if err != nil || res.Total == nil || *res.Total != 2 || len(res.Data) != 1 {
		t.Fatalf("total: %+v %v", res, err)
	}
	first, err := svc.List(ctx, ListFilter{Metadata: map[string]string{"tier": "gold"}, Limit: 1})
	if err != nil || first.NextCursor == nil {
		t.Fatalf("cursor page 1: %+v %v", first, err)
	}
	second, err := svc.List(ctx, ListFilter{Metadata: map[string]string{"tier": "gold"}, Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Data) != 1 || second.Data[0].ID == first.Data[0].ID || second.NextCursor != nil {
		t.Fatalf("cursor page 2: %+v %v", second, err)
	}
	// Validation.
	if _, err := svc.List(ctx, ListFilter{Metadata: map[string]string{"a": "1", "b": "1", "c": "1", "d": "1"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("four filters: %v", err)
	}
	if _, err := svc.List(ctx, ListFilter{Metadata: map[string]string{"$where": "1"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad key: %v", err)
	}
}

func TestIntegrationRuleAccountCarriesMetadata(t *testing.T) {
	svc, ctx, org := testSvc(t)
	in := sampleCreate("rules-1")
	in.Metadata = map[string]any{"crm": "x", "tier": float64(2)}
	a, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RuleAccount(ctx, svc.DB, org, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got["metadata"].(map[string]any)
	if !ok || m["crm"] != "x" || m["tier"] != float64(2) {
		t.Fatalf("rule account = %#v", got)
	}
	if got["externalId"] != "rules-1" || got["id"] != a.ID || got["email"] != "test@example.com" {
		t.Fatalf("rule account = %#v", got)
	}
	plain, err := svc.Create(ctx, sampleCreate("rules-2"))
	if err != nil {
		t.Fatal(err)
	}
	got, err = RuleAccount(ctx, svc.DB, org, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := got["metadata"].(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("no metadata → empty map, got %#v", got["metadata"])
	}
	// Another organisation never sees the account.
	if got, err := RuleAccount(ctx, svc.DB, "org_other", a.ID); err != nil || len(got) != 0 {
		t.Fatalf("cross-org = %#v %v", got, err)
	}
}
