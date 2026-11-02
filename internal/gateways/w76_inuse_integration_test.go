//go:build integration

package gateways

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestDeleteRefusesWhenAccountsReferenceGateway(t *testing.T) {
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
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Egress: egress.NewDialer(egress.Policy{}),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: 0}, EHLO: "eb.test",
	}
	g, err := svc.Create(cctx, CreateInput{
		Name: "relay-in-use", Host: "127.0.0.1", Port: 2525, TLS: TLSNone,
		Auth: AuthInput{User: "u", Pass: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("accounts").InsertOne(ctx, map[string]any{
		"_id": store.NewID(store.PrefixAccount), "organizationId": org,
		"smtp": map[string]any{"gatewayId": g.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Delete(cctx, g.ID, "")
	if errs.CodeOf(err) != errs.CodeSMTPGatewayInUse {
		t.Fatalf("want smtp_gateway_in_use, got %v", err)
	}
}
