//go:build integration

package oauth

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationW38_serviceKeyTenantAppOnly(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(32).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Log: quiet,
		now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	sk := `{"type":"service_account","client_email":"sa@proj.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n"}`
	appOnly := true
	a, err := svc.Create(cctx, CreateInput{
		Name: "Gmail SA", Provider: ProviderGmail, ClientID: "cid", ClientSecret: "sec",
		ServiceKey: sk, AppOnly: &appOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := ViewOf(a)
	if !v.HasServiceKey || !v.HasClientSecret {
		t.Fatalf("%+v", v)
	}
	raw, err := svc.ServiceAccountJSON(cctx, a.ID)
	if err != nil || string(raw) != sk {
		t.Fatalf("%v %q", err, raw)
	}

	tenant := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	ao := true
	a2, err := svc.Create(cctx, CreateInput{
		Name: "Outlook app-only", Provider: ProviderOutlook, ClientID: "ms-cid", ClientSecret: "ms-sec",
		TenantID: tenant, AppOnly: &ao,
	})
	if err != nil {
		t.Fatal(err)
	}
	mat, err := svc.ClientCredentialsMaterial(cctx, a2)
	if err != nil || mat.TenantID != tenant || !mat.AppOnly || mat.ClientID != "ms-cid" || mat.Secret != "ms-sec" {
		t.Fatalf("%+v %v", mat, err)
	}
	if !strings.Contains(mat.TokenURL(), tenant) {
		t.Fatal(mat.TokenURL())
	}
	_, tok, _ := resolveEndpoints(a2)
	if !strings.Contains(tok, tenant) {
		t.Fatal(tok)
	}

	patchedTenant := "common"
	newSK := `{"type":"service_account","client_email":"sa2@proj.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nMIIF\n-----END PRIVATE KEY-----\n"}`
	a2, err = svc.Patch(cctx, a2.ID, formatETag(a2.Version), PatchInput{
		TenantID: &patchedTenant, ServiceKey: &newSK, AppOnly: &ao,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a2.TenantID != "common" || a2.ServiceKey.IsZero() {
		t.Fatalf("%+v", a2)
	}
	bad := `{"type":"user"}`
	if _, err := svc.Patch(cctx, a2.ID, formatETag(a2.Version), PatchInput{ServiceKey: &bad}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.ClientCredentialsMaterial(cctx, nil); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.ServiceAccountJSON(cctx, store.NewID(store.PrefixOAuthApp)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	a3, err := svc.Create(cctx, CreateInput{
		Name: "No SA", Provider: ProviderGmail, ClientID: "c2", ClientSecret: "s2",
		TenantID: "organizations",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ServiceAccountJSON(cctx, a3.ID); errs.CodeOf(err) != errs.CodeOauthNotConnected {
		t.Fatal(err)
	}
	emptySK := "   "
	a3, err = svc.Patch(cctx, a3.ID, formatETag(a3.Version), PatchInput{ServiceKey: &emptySK})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	tid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if _, err := svc.Patch(cctx, a3.ID, formatETag(a3.Version), PatchInput{TenantID: &tid, AppOnly: &off}); err != nil {
		t.Fatal(err)
	}
}
