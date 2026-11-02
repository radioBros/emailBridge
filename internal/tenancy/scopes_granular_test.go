package tenancy

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestHasScopeOnAndRequireOn(t *testing.T) {
	acc1 := store.NewID(store.PrefixAccount)
	acc2 := store.NewID(store.PrefixAccount)
	whe1 := store.NewID(store.PrefixEndpoint)

	orgWide := Caller{Kind: CallerAPIKey, APIKeyID: "key_a", OrganizationID: "org_1", Scopes: []string{"accounts:write"}}
	granular := Caller{Kind: CallerAPIKey, APIKeyID: "key_b", OrganizationID: "org_1", Scopes: []string{"accounts:write:" + acc1}}
	admin := Caller{Kind: CallerAPIKey, APIKeyID: "key_c", OrganizationID: "org_1", Scopes: []string{"admin"}}
	webhookKey := Caller{Kind: CallerAPIKey, APIKeyID: "key_d", OrganizationID: "org_1", Scopes: []string{"webhooks:read:" + whe1}}
	bound := Caller{Kind: CallerAPIKey, APIKeyID: "key_e", OrganizationID: "org_1", Scopes: []string{"accounts:write"}, AccountID: acc1}

	if !orgWide.Allows(PermAccountsWrite) || !orgWide.AllowsOn(PermAccountsWrite, acc2) {
		t.Fatal("org-wide should cover every account")
	}
	if granular.Allows(PermAccountsWrite) {
		t.Fatal("resource-only must not pass org-wide Require")
	}
	if !granular.AllowsOn(PermAccountsWrite, acc1) || granular.AllowsOn(PermAccountsWrite, acc2) {
		t.Fatal("granular allow matrix")
	}
	if !admin.AllowsOn(PermWebhooksWrite, whe1) {
		t.Fatal("admin")
	}
	if !webhookKey.AllowsOn(PermRead, whe1) || webhookKey.AllowsOn(PermRead, store.NewID(store.PrefixEndpoint)) {
		t.Fatal("webhook read on")
	}

	ctx := WithCaller(context.Background(), granular)
	if err := Require(ctx, PermAccountsWrite); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("Require org-wide: %v", err)
	}
	if err := RequireOn(ctx, PermAccountsWrite, acc1); err != nil {
		t.Fatal(err)
	}
	if err := RequireOn(ctx, PermAccountsWrite, acc2); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("other account: %v", err)
	}

	bctx := WithCaller(context.Background(), bound)
	if err := RequireAccount(bctx, PermAccountsWrite, acc1); err != nil {
		t.Fatal(err)
	}
	if err := RequireAccount(bctx, PermAccountsWrite, acc2); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound: %v", err)
	}
}

func TestScopedIDs(t *testing.T) {
	acc1 := store.NewID(store.PrefixAccount)
	acc2 := store.NewID(store.PrefixAccount)
	cases := []struct {
		name    string
		scopes  []string
		kind    string
		limited bool
		n       int
	}{
		{"admin", []string{"admin"}, "accounts", false, 0},
		{"org-wide read", []string{"accounts:read"}, "accounts", false, 0},
		{"org-wide write", []string{"accounts:write"}, "accounts", false, 0},
		{"two ids", []string{"accounts:read:" + acc1, "accounts:write:" + acc2}, "accounts", true, 2},
		{"empty limited", []string{"logs:read"}, "accounts", true, 0},
		{"webhooks", []string{"webhooks:write:" + store.NewID(store.PrefixEndpoint)}, "webhooks", true, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := WithCaller(context.Background(), Caller{Kind: CallerAPIKey, Scopes: c.scopes})
			ids, limited := ScopedIDs(ctx, c.kind)
			if limited != c.limited || len(ids) != c.n {
				t.Fatalf("ids=%v limited=%v want n=%d limited=%v", ids, limited, c.n, c.limited)
			}
		})
	}
	// non-API-key → unrestricted
	ctx := WithCaller(context.Background(), Caller{Kind: CallerUser, Role: RoleViewer})
	if _, limited := ScopedIDs(ctx, "accounts"); limited {
		t.Fatal("user must not be limited by ScopedIDs")
	}
}

func TestHasScopeOrgWideIgnoresGranular(t *testing.T) {
	acc := store.NewID(store.PrefixAccount)
	if hasScope([]string{"accounts:write:" + acc}, "accounts:write") {
		t.Fatal("granular must not satisfy org-wide want")
	}
	if !hasScopeOn([]string{"accounts:write:" + acc}, "accounts:write", acc) {
		t.Fatal("granular should satisfy on")
	}
	if !hasScopeOn([]string{"accounts:write"}, "accounts:read", acc) {
		t.Fatal("org write implies read on")
	}
}
