//go:build integration

package oauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestDeleteRefusesWhenAccountsReferenceApp(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Log: quiet,
		CallbackURL: "https://eb.test/oauth/callback", PublicURL: "https://eb.test",
	}
	app, err := svc.Create(cctx, CreateInput{
		Name: "in-use", Provider: ProviderGmail, ClientID: "cid", ClientSecret: "sec",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("accounts").InsertOne(ctx, map[string]any{
		"_id": store.NewID(store.PrefixAccount), "organizationId": org,
		"oauth": map[string]any{"appId": app.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Delete(cctx, app.ID, "")
	if errs.CodeOf(err) != errs.CodeOauthAppInUse {
		t.Fatalf("want oauth_app_in_use, got %v", err)
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Safe["count"] == nil {
		t.Fatalf("missing count detail: %v", err)
	}
}
