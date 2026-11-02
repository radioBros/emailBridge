//go:build integration

package tenancy

import (
	"context"
	"testing"
	"time"
)

// Members list the user's name and last sign-in (S13 §4.1 Members), and TouchLogin stamps the sign-in.
func TestIntegrationMemberProfilesAndTouchLogin(t *testing.T) {
	svc, repo, _, _ := orgHarness(t)
	ctx := context.Background()
	owner := &User{AuthUserID: "auth-owner", Email: "owner@t.test", Name: "Ada Owner", InstanceRole: "owner"}
	if err := repo.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	org, err := repo.CreateOrganization(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, org.ID, owner.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	oc := ctxAs(org.ID, owner.ID, RoleOwner, false)
	if _, err := svc.InviteMember(oc, "pending@t.test", RoleViewer); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListMembers(oc, ListFilter{Limit: 10})
	if err != nil || len(list.Data) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	for _, m := range list.Data {
		switch m.UserID {
		case owner.ID:
			if m.Name != "Ada Owner" || m.LastLoginAt != nil || m.Email != "owner@t.test" {
				t.Fatalf("owner before sign-in: %+v", m)
			}
		default:
			if m.Name != "" || m.Email != "pending@t.test" {
				t.Fatalf("pending invite: %+v", m)
			}
		}
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := svc.TouchLogin(ctx, "auth-owner"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TouchLogin(ctx, "unknown-auth-id"); err != nil {
		t.Fatalf("an unknown user is not an error: %v", err)
	}
	list, err = svc.ListMembers(oc, ListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list.Data {
		if m.UserID == owner.ID && (m.LastLoginAt == nil || m.LastLoginAt.Before(before)) {
			t.Fatalf("lastLoginAt not recorded: %+v", m)
		}
	}
}
