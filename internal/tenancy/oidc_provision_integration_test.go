//go:build integration

package tenancy

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationHasPendingInvite(t *testing.T) {
	db := testDB(t)
	svc := &Service{Repo: &Repo{DB: db}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	org, err := svc.Repo.CreateOrganization(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := svc.HasPendingInvite(ctx, "invitee@t.test")
	if err != nil || ok {
		t.Fatalf("empty: %v %v", ok, err)
	}
	now := time.Now().UTC()
	if err := svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: "invitee@t.test",
		Role: RoleViewer, Invite: &Invite{Email: "invitee@t.test", TokenHash: "h1", ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = svc.HasPendingInvite(ctx, "Invitee@T.TEST")
	if err != nil || !ok {
		t.Fatalf("pending: %v %v", ok, err)
	}
	if err := svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: "late@t.test",
		Role: RoleViewer, Invite: &Invite{Email: "late@t.test", TokenHash: "h2", ExpiresAt: now.Add(-time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = svc.HasPendingInvite(ctx, "late@t.test")
	if err != nil || ok {
		t.Fatalf("expired must be false: %v %v", ok, err)
	}
}

func TestIntegrationProvisionFromGroups(t *testing.T) {
	db := testDB(t)
	svc := &Service{Repo: &Repo{DB: db}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	org, err := svc.Repo.CreateOrganization(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	org2, err := svc.Repo.CreateOrganization(ctx, "Beta", "beta")
	if err != nil {
		t.Fatal(err)
	}
	authID := "auth-sso-1"
	email := "sso@t.test"
	roles := map[string]string{
		"admins": org.ID + ":admin",
		"ops":    org2.ID + ":operator",
	}
	if err := svc.ProvisionFromGroups(ctx, authID, email, "oidc_p1", roles, []string{"admins", "ops"}); err != nil {
		t.Fatal(err)
	}
	u, err := svc.Repo.UserByAuthID(ctx, authID)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := svc.Repo.MembershipsOf(ctx, u.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("memberships: %+v %v", ms, err)
	}
	byOrg := map[string]Role{}
	for _, m := range ms {
		byOrg[m.OrganizationID] = m.Role
	}
	if byOrg[org.ID] != RoleAdmin || byOrg[org2.ID] != RoleOperator {
		t.Fatalf("roles %+v", byOrg)
	}
	// idempotent re-login
	if err := svc.ProvisionFromGroups(ctx, authID, email, "oidc_p1", roles, []string{"admins", "ops"}); err != nil {
		t.Fatal(err)
	}
	ms2, err := svc.Repo.MembershipsOf(ctx, u.ID)
	if err != nil || len(ms2) != 2 {
		t.Fatalf("after re-login: %+v %v", ms2, err)
	}
}

func TestIntegrationProvisionInviteAndNoOwnerDowngrade(t *testing.T) {
	db := testDB(t)
	svc := &Service{Repo: &Repo{DB: db}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	org, err := svc.Repo.CreateOrganization(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	email := "invited@t.test"
	if err := svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: email,
		Role: RoleViewer, Invite: &Invite{Email: email, TokenHash: "tok", ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	authID := "auth-invited"
	if err := svc.ProvisionFromGroups(ctx, authID, email, "oidc_p1", nil, nil); err != nil {
		t.Fatal(err)
	}
	u, err := svc.Repo.UserByAuthID(ctx, authID)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := svc.Repo.MembershipsOf(ctx, u.ID)
	if err != nil || len(ms) != 1 || ms[0].Role != RoleViewer || ms[0].AcceptedAt == nil {
		t.Fatalf("invite accepted: %+v %v", ms, err)
	}
	// promote to owner then refuse downgrade via groups
	_, err = svc.Repo.UpdateMembership(ctx, ms[0].ID, ms[0].Version, bson.M{"role": RoleOwner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{"g": org.ID + ":viewer"}
	if err := svc.ProvisionFromGroups(ctx, authID, email, "oidc_p1", roles, []string{"g"}); err != nil {
		t.Fatal(err)
	}
	ms, err = svc.Repo.MembershipsOf(ctx, u.ID)
	if err != nil || len(ms) != 1 || ms[0].Role != RoleOwner {
		t.Fatalf("owner must stay: %+v %v", ms, err)
	}
	// upgrade viewer→admin works when not owner
	orgB, _ := svc.Repo.CreateOrganization(ctx, "Beta", "beta2")
	mem, err := svc.Repo.CreateMembership(ctx, orgB.ID, u.ID, RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	roles2 := map[string]string{"g2": orgB.ID + ":admin"}
	if err := svc.ProvisionFromGroups(ctx, authID, email, "oidc_p1", roles2, []string{"g2"}); err != nil {
		t.Fatal(err)
	}
	var got Membership
	if err := svc.Repo.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{"_id": mem.ID}).Decode(&got); err != nil || got.Role != RoleAdmin {
		t.Fatalf("upgrade: %+v %v", got, err)
	}
}
