//go:build integration

package apikeys

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationAPIKeySearchQ(t *testing.T) {
	h := newHarness(t)
	ctx := h.asOwner()

	k1, _, err := h.svc.Create(ctx, CreateInput{Name: "Alpha Key", Scopes: []string{"accounts:read"}})
	if err != nil {
		t.Fatal(err)
	}
	k2, _, err := h.svc.Create(ctx, CreateInput{Name: "Other", Scopes: []string{"accounts:read"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = h.svc.Create(ctx, CreateInput{Name: "Noise", Scopes: []string{"accounts:read"}})
	if err != nil {
		t.Fatal(err)
	}

	byName, err := h.svc.List(ctx, ListFilter{Q: "alpha", Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(byName.Data) != 1 || byName.Data[0].ID != k1.ID || byName.Total == nil || *byName.Total != 1 {
		t.Fatalf("name q: %+v %v", byName, err)
	}
	// All keys share prefix "ebk_"; searching it must match on the prefix field (here: all three).
	byPrefix, err := h.svc.List(ctx, ListFilter{Q: k2.Prefix, Limit: 10})
	if err != nil || len(byPrefix.Data) != 3 {
		t.Fatalf("prefix q: %+v %v", byPrefix, err)
	}
	// Distinct last4 is also exposed via name-or-prefix; plant a unique prefix for a precise match.
	unique := "ebk_uniq_" + k2.Last4
	if _, err := h.db.Collection(Collection).UpdateOne(ctx, bson.M{"_id": k2.ID}, bson.M{"$set": bson.M{"prefix": unique}}); err != nil {
		t.Fatal(err)
	}
	byUnique, err := h.svc.List(ctx, ListFilter{Q: "uniq_" + k2.Last4, Limit: 10})
	if err != nil || len(byUnique.Data) != 1 || byUnique.Data[0].ID != k2.ID {
		t.Fatalf("unique prefix q: %+v %v", byUnique, err)
	}
	literal, err := h.svc.List(ctx, ListFilter{Q: ".*", Limit: 10})
	if err != nil || len(literal.Data) != 0 {
		t.Fatalf("regex metachar must match literally: %+v %v", literal, err)
	}
	if _, err := h.svc.List(ctx, ListFilter{Q: string(make([]byte, 201))}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}
}

func TestIntegrationGranularScopesCreatePatch(t *testing.T) {
	h := newHarness(t)
	ctx := h.asOwner()
	acc := store.NewID(store.PrefixAccount)
	missing := store.NewID(store.PrefixAccount)
	_, err := h.db.Collection("accounts").InsertOne(ctx, bson.M{
		"_id": acc, "organizationId": h.org, "name": "A", "email": "a@example.test",
		"createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = h.svc.Create(ctx, CreateInput{
		Name: "bad-resource", Scopes: []string{"accounts:write:" + missing},
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("missing resource: %v", err)
	}

	k, tok, err := h.svc.Create(ctx, CreateInput{
		Name: "granular", Scopes: []string{"accounts:write:" + acc, "apikeys:write"},
	})
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	c, err := h.resolveThroughHTTP(tok, "", "")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	kctx := tenancy.WithCaller(ctx, *c)
	if err := tenancy.RequireOn(kctx, tenancy.PermAccountsWrite, acc); err != nil {
		t.Fatal(err)
	}
	if err := tenancy.RequireOn(kctx, tenancy.PermAccountsWrite, missing); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("other account: %v", err)
	}
	if err := tenancy.Require(kctx, tenancy.PermAccountsWrite); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("org-wide Require: %v", err)
	}
	ids, limited := tenancy.ScopedIDs(kctx, "accounts")
	if !limited || len(ids) != 1 || ids[0] != acc {
		t.Fatalf("ScopedIDs: %v limited=%v", ids, limited)
	}

	// patch to a different missing id → validation_failed; cache still purged on successful patch
	bad := []string{"accounts:read:" + missing}
	if _, err := h.svc.Patch(ctx, k.ID, PatchInput{Scopes: &bad}, formatETag(k.Version)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("patch missing: %v", err)
	}
	okScopes := []string{"accounts:read:" + acc}
	upd, err := h.svc.Patch(ctx, k.ID, PatchInput{Scopes: &okScopes}, formatETag(k.Version))
	if err != nil || upd.Version != 2 {
		t.Fatal(err)
	}
	c2, err := h.resolveThroughHTTP(tok, "", "")
	if err != nil || c2 == nil || len(c2.Scopes) != 1 || c2.Scopes[0] != okScopes[0] {
		t.Fatalf("after patch resolve: %+v %v", c2, err)
	}
}
