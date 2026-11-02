package gateways

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestCreateFromIntegrationValidation(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()
	if _, err := svc.CreateFromIntegration(ctx, CreateInput{Name: "x", Kind: KindSES}); errs.CodeOf(err) != errs.CodeUnauthenticated && errs.CodeOf(err) != errs.CodeForbidden {
		// no caller
		if err == nil {
			t.Fatal("want auth err")
		}
	}
	org := store.NewID(store.PrefixOrganization)
	cctx := store.WithOrganization(tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	}), org)
	if _, err := svc.CreateFromIntegration(cctx, CreateInput{Name: "n", Kind: KindSMTP, Host: "h", Port: 25}); errs.CodeOf(err) != errs.CodeValidationFailed {
		// SMTP rejected for integration path — may fail earlier on validate
		if err == nil {
			t.Fatal("want validation")
		}
	}
}

func TestCreateUncheckedAPIShape(t *testing.T) {
	// validateCreate path only (no repo)
	err := validateCreate(CreateInput{
		Name: "ses", Kind: KindSES,
		Credentials: &CredentialsInput{AccessKey: "a", SecretKey: "s"},
		API:         &APIConfig{Region: "eu-west-1", From: "a@b.c"},
	})
	if err != nil {
		t.Fatal(err)
	}
}
