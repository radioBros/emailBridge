package oauth

import (
	"context"
	"testing"

	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestLinkAppOnly_guards(t *testing.T) {
	svc := &Service{Repo: &Repo{}}
	if err := svc.LinkAppOnly(context.Background(), store.NewID(store.PrefixAccount), store.NewID(store.PrefixOAuthApp), true); err == nil {
		t.Fatal("no perm")
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	if err := svc.LinkAppOnly(ctx, "bad", store.NewID(store.PrefixOAuthApp), true); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if err := svc.LinkAppOnly(ctx, store.NewID(store.PrefixAccount), "bad", true); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatal(err)
	}
	// Caller with write role but empty OrganizationID.
	noOrg := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), Role: tenancy.RoleOwner,
	})
	if err := svc.LinkAppOnly(noOrg, store.NewID(store.PrefixAccount), store.NewID(store.PrefixOAuthApp), true); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
}

func TestMintProbe_unknownMode(t *testing.T) {
	svc := &Service{}
	if _, _, _, err := svc.mintAppOnly(context.Background(), &App{}, "nope", "u@ex.com"); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatal(err)
	}
	if err := svc.probeAppOnly(context.Background(), "nope", "u@ex.com", "t"); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatal(err)
	}
}

func TestStoreAppOnlyTokens_emptyToken(t *testing.T) {
	svc := &Service{}
	err := svc.storeAppOnlyTokens(context.Background(), "org", "acc", &App{ID: "oap"}, AuthModeAppOnly, &oauth2.Token{})
	if errs.CodeOf(err) != errs.CodeOauthExchangeFailed {
		t.Fatal(err)
	}
}

func TestEnsureAppOnly_nilOAuth(t *testing.T) {
	svc := &Service{Repo: &Repo{}}
	if _, err := svc.ensureAppOnlyStillValid(context.Background(), &accountOAuthDoc{}); errs.CodeOf(err) != errs.CodeOauthNotConnected {
		t.Fatal(err)
	}
}
