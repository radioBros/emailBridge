//go:build integration

package tenancy

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestIntegrationMemberSearchQ(t *testing.T) {
	svc, repo, _, _ := orgHarness(t)
	ctx := context.Background()
	owner := &User{AuthUserID: "auth-owner", Email: "owner@t.test", Name: "Ada Owner", InstanceRole: "owner"}
	if err := repo.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	named := &User{AuthUserID: "auth-bob", Email: "bob@t.test", Name: "Bob Builder"}
	if err := repo.CreateUser(ctx, named); err != nil {
		t.Fatal(err)
	}
	org, err := repo.CreateOrganization(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, org.ID, owner.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, org.ID, named.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	oc := ctxAs(org.ID, owner.ID, RoleOwner, false)
	if _, err := svc.InviteMember(oc, "pending.unique@t.test", RoleViewer); err != nil {
		t.Fatal(err)
	}

	byInvite, err := svc.ListMembers(oc, ListFilter{Q: "pending.unique", Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(byInvite.Data) != 1 || byInvite.Total == nil || *byInvite.Total != 1 {
		t.Fatalf("invite email q: %+v %v", byInvite, err)
	}
	byUserEmail, err := svc.ListMembers(oc, ListFilter{Q: "bob@t", Limit: 10})
	if err != nil || len(byUserEmail.Data) != 1 || byUserEmail.Data[0].UserID != named.ID {
		t.Fatalf("user email q: %+v %v", byUserEmail, err)
	}
	byName, err := svc.ListMembers(oc, ListFilter{Q: "Builder", Limit: 10})
	if err != nil || len(byName.Data) != 1 || byName.Data[0].UserID != named.ID {
		t.Fatalf("user name q: %+v %v", byName, err)
	}
	literal, err := svc.ListMembers(oc, ListFilter{Q: ".*", Limit: 10})
	if err != nil || len(literal.Data) != 0 {
		t.Fatalf("regex metachar must match literally: %+v %v", literal, err)
	}
	if _, err := svc.ListMembers(oc, ListFilter{Q: string(make([]byte, 201))}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}
}
