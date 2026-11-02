//go:build integration

package oauth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationW54ProviderMismatchAndOAuthView(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	_, srv := startMockProvider(t)
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		State: &memState{}, Leases: &memLease{}, NodeID: "n1",
		CallbackURL: "https://cb.example", PublicURL: "https://eb.example",
		HTTP: srv.Client(), Log: quiet,
	}

	gmailApp, err := svc.Create(cctx, CreateInput{
		Name: "G", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	graphAcc := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": graphAcc, "organizationId": org, "provider": accounts.ProviderGraph,
		"email": "g@ex.com", "state": accounts.StateNew,
	})
	if _, err := svc.Start(cctx, graphAcc, StartInput{AppID: gmailApp.ID}); errs.CodeOf(err) != errs.CodeOauthProviderMismatch {
		t.Fatalf("want mismatch, got %v", err)
	}

	imapAcc := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": imapAcc, "organizationId": org, "provider": accounts.ProviderIMAP,
		"email": "i@ex.com", "state": accounts.StateNew,
		"oauth": AccountOAuth{AppID: gmailApp.ID, ExpiresAt: time.Now().UTC().Add(time.Hour), Scopes: []string{"mail"}},
	})
	view, err := svc.GetAccountOAuth(cctx, imapAcc)
	if err != nil || !view.Connected || view.AppID != gmailApp.ID {
		t.Fatalf("view %#v %v", view, err)
	}
	empty := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": empty, "organizationId": org, "email": "e@ex.com", "state": accounts.StateNew,
	})
	v2, err := svc.GetAccountOAuth(cctx, empty)
	if err != nil || v2.Connected {
		t.Fatalf("empty %#v %v", v2, err)
	}

	otherOrgID := store.NewID(store.PrefixOrganization)
	otherOrg := tenancy.WithCaller(store.WithOrganization(ctx, otherOrgID), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: otherOrgID, Role: tenancy.RoleOwner,
	})
	if _, err := svc.GetAccountOAuth(otherOrg, imapAcc); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant %v", err)
	}
}

func TestIntegrationW54TokenExportAndSignatures(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	_, tokSrv := startMockProvider(t)

	sendAs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/settings/sendAs") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sendAs": []map[string]any{{
				"sendAsEmail": "a@ex.com", "displayName": "A", "isDefault": true, "isPrimary": true,
				"signature": "<b>Hi</b>",
			}},
		})
	}))
	defer sendAs.Close()

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:       &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		State:       &memState{},
		Leases:      &memLease{},
		NodeID:      "n1",
		CallbackURL: "https://cb.example", PublicURL: "https://eb.example",
		HTTP: tokSrv.Client(), Log: quiet, GmailAPIRoot: sendAs.URL,
	}
	app, err := svc.Create(cctx, CreateInput{
		Name: "G2", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: tokSrv.URL + "/authorize", TokenURL: tokSrv.URL + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := store.NewID(store.PrefixAccount)
	at, err := svc.Cipher.EncryptString(ctx, org, "at-live", crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.accessToken", DocID: accID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": accID, "organizationId": org, "provider": accounts.ProviderGmail,
		"email": "a@ex.com", "state": accounts.StateConnected,
		"imap": bson.M{"auth": bson.M{"user": "a@ex.com"}},
		"oauth": AccountOAuth{
			AppID: app.ID, AccessToken: at, ExpiresAt: time.Now().UTC().Add(time.Hour), Scopes: []string{"mail"},
		},
	})

	if _, err := svc.ExportAccessToken(cctx, accID); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("export off: %v", err)
	}
	_, _ = db.Collection(settings.SettingsCollection).InsertOne(ctx, settings.Doc{
		ID: settings.OrgDocID(org),
		Switches: map[string]settings.SwitchValue{
			"oauth.tokenExport": {Value: true},
		},
	})
	exp, err := svc.ExportAccessToken(cctx, accID)
	if err != nil || exp.AccessToken == "" || exp.TokenType != "Bearer" {
		t.Fatalf("export %#v %v", exp, err)
	}

	viewer := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if _, err := svc.ExportAccessToken(viewer, accID); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer %v", err)
	}

	sigs, err := svc.ListServerSignatures(cctx, accID)
	if err != nil || len(sigs.Data) != 1 || sigs.Data[0].Signature != "<b>Hi</b>" {
		t.Fatalf("sigs %#v %v", sigs, err)
	}

	graphAcc := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": graphAcc, "organizationId": org, "provider": accounts.ProviderGraph,
		"email": "g@ex.com", "state": accounts.StateConnected,
	})
	gs, err := svc.ListServerSignatures(cctx, graphAcc)
	if err != nil || gs.Reason != "graph_signatures_unavailable" || len(gs.Data) != 0 {
		t.Fatalf("graph %#v %v", gs, err)
	}

	imapAcc := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": imapAcc, "organizationId": org, "provider": accounts.ProviderIMAP,
		"email": "i@ex.com", "state": accounts.StateConnected,
	})
	if _, err := svc.ListServerSignatures(cctx, imapAcc); errs.CodeOf(err) != errs.CodeUnsupportedForProvider {
		t.Fatalf("imap %v", err)
	}
}
