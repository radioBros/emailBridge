//go:build integration

package connect

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// returnUrlHosts round-trips through MongoDB (stored as a BSON array) and stays per organisation.
func TestIntegrationReturnURLHostsMongo(t *testing.T) {
	db := testMongo(t)
	ctx := context.Background()
	if err := EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	for _, id := range []string{orgA, orgB} {
		if _, err := db.Collection("organizations").InsertOne(ctx, orgDoc{ID: id, Name: "Org", Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(&Repo{DB: db}, nil)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	as := func(org string) context.Context {
		return tenancy.WithCaller(ctx, tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
		})
	}
	hosts := []string{"app.integrator.example"}
	if _, _, err := s.UpdateBranding(as(orgA), BrandingUpdate{ReturnURLHosts: &hosts}, ""); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.GetBranding(as(orgA))
	if err != nil || len(b.ReturnURLHosts) != 1 || b.ReturnURLHosts[0] != "app.integrator.example" {
		t.Fatalf("round trip: %+v %v", b, err)
	}
	if _, err := s.CreateHosted(as(orgA), HostedCreate{ReturnURL: "https://app.integrator.example/back"}); err != nil {
		t.Fatalf("org A listed host: %v", err)
	}
	if _, err := s.CreateHosted(as(orgB), HostedCreate{ReturnURL: "https://app.integrator.example/back"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("org B must not inherit org A's list: %v", err)
	}
	if _, err := s.CreateHosted(as(orgA), HostedCreate{ReturnURL: "http://app.integrator.example/back"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("http on a listed host: %v", err)
	}
}
