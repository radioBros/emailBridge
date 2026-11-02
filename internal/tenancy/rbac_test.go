package tenancy

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// TestRoleMatrix is the S10 §4 table, row by row.
func TestRoleMatrix(t *testing.T) {
	type row struct {
		p                              Permission
		owner, admin, operator, viewer bool
	}
	rows := []row{
		{PermRead, true, true, true, true},
		{PermAccountsWrite, true, true, true, false},
		{PermAccountsOperate, true, true, true, false},
		{PermSend, true, true, true, false},
		{PermWebhooksWrite, true, true, true, false},
		{PermProtocolLogs, true, true, false, false},
		{PermSettingsWrite, true, true, false, false},
		{PermAPIKeysWrite, true, true, false, false},
		{PermMembersWrite, true, true, false, false},
		{PermAuditRead, true, true, true, false},
		{PermLogsRead, true, true, true, false},
		{PermOrgDelete, true, false, false, false},
		{PermInstanceAdmin, false, false, false, false},
	}
	for _, r := range rows {
		for role, want := range map[Role]bool{RoleOwner: r.owner, RoleAdmin: r.admin, RoleOperator: r.operator, RoleViewer: r.viewer} {
			if got := RoleAllows(role, r.p); got != want {
				t.Errorf("%s/%s = %v, want %v", role, r.p, got, want)
			}
		}
	}
	if !RoleOwner.Valid() || Role("root").Valid() {
		t.Error("Valid")
	}
	if len(Permissions(RoleViewer)) != 1 {
		t.Error("viewer permissions")
	}
}

func TestCallerAllowsAndRequire(t *testing.T) {
	ctx := context.Background()
	if err := Require(ctx, PermRead); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("no caller: %v", err)
	}
	owner := Caller{Kind: CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: RoleOwner, InstanceOwner: true}
	viewer := Caller{Kind: CallerUser, UserID: "usr_2", OrganizationID: "org_1", Role: RoleViewer}
	key := Caller{Kind: CallerAPIKey, APIKeyID: "key_1", OrganizationID: "org_1", Scopes: []string{"accounts:write", "logs:read"}, AccountID: "acc_1"}
	admin := Caller{Kind: CallerAPIKey, APIKeyID: "key_2", OrganizationID: "org_1", Scopes: []string{"admin"}}
	sys := Caller{Kind: CallerSystem}
	cases := []struct {
		c    Caller
		p    Permission
		want bool
	}{
		{owner, PermInstanceAdmin, true}, {viewer, PermInstanceAdmin, false}, {viewer, PermAccountsWrite, false},
		{key, PermAccountsWrite, true}, {key, PermAccountsOperate, true}, {key, PermLogsRead, true},
		// A bare PermRead is admin-only for keys: reads name their scope (RequireRead), so a forgotten scope fails closed.
		{key, PermRead, false}, {admin, PermRead, true},
		{key, PermAuditRead, false}, {key, PermOrgDelete, false}, {key, PermInstanceAdmin, false},
		{admin, PermSettingsWrite, true}, {admin, PermOrgDelete, false},
		{Caller{Kind: CallerAPIKey, Scopes: []string{"audit:write"}}, PermAuditRead, true},
		{Caller{Kind: CallerAPIKey}, PermRead, false},
		{sys, PermOrgDelete, true}, {Caller{Kind: "alien"}, PermRead, false},
	}
	for _, c := range cases {
		if got := c.c.Allows(c.p); got != c.want {
			t.Errorf("%s %v/%s = %v want %v", c.c.Kind, c.c.Scopes, c.p, got, c.want)
		}
	}
	if err := Require(WithCaller(ctx, viewer), PermAccountsWrite); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer: %v", err)
	}
	if err := Require(WithCaller(ctx, key), PermAuditRead); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("key scope: %v", err)
	}
	kctx := WithCaller(ctx, key)
	if err := RequireAccount(kctx, PermAccountsWrite, "acc_1"); err != nil {
		t.Fatal(err)
	}
	if err := RequireAccount(kctx, PermAccountsWrite, "acc_2"); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key: %v", err)
	}
	if err := RequireAccount(kctx, PermAuditRead, "acc_1"); err == nil {
		t.Fatal("scope checked first")
	}
	// context side effects
	if org, ok := store.OrganizationFrom(kctx); !ok || org != "org_1" {
		t.Fatal("tenant guard org")
	}
	if ids := logging.IDsFrom(kctx); ids.Actor != "key_1" || ids.OrganizationID != "org_1" {
		t.Fatalf("log ids %+v", ids)
	}
	if owner.Actor() != "usr_1" || sys.Actor() != "system" {
		t.Fatal("actors")
	}
	if c, ok := CallerFrom(kctx); !ok || c.APIKeyID != "key_1" {
		t.Fatal("CallerFrom")
	}
	if _, ok := CallerFrom(WithCaller(ctx, sys)); !ok {
		t.Fatal("system caller without org")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Acme S.p.A.": "acme-s-p-a", "  X ": "org-x", "Già Più": "gi-pi",
		"a very long organisation name that goes on and on and on forever": "a-very-long-organisation-name-that-goes-on-and-o"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q want %q", in, got, want)
		}
	}
}
