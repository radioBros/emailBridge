package oauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestValidateServiceKeyAndTenant(t *testing.T) {
	if err := validateServiceKeyJSON(`not-json`); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateServiceKeyJSON(`{"type":"user","client_email":"a@b","private_key":"k"}`); err == nil {
		t.Fatal("type")
	}
	if err := validateServiceKeyJSON(`{"type":"service_account","client_email":"","private_key":"k"}`); err == nil {
		t.Fatal("email")
	}
	if err := validateServiceKeyJSON(`{"type":"service_account","client_email":"a@b.c","private_key":"-----BEGIN PRIVATE KEY-----\nX\n-----END PRIVATE KEY-----\n"}`); err != nil {
		t.Fatal(err)
	}
	if err := validateServiceKeyJSON(`{"type":"service_account","client_email":"a@b.c","private_key":""}`); err == nil {
		t.Fatal("private_key")
	}
	if err := validateCreate(CreateInput{
		Name: "x", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		ServiceKey: `{bad`,
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateTenantID("nope"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	for _, ok := range []string{"", "common", "organizations", "11111111-2222-3333-4444-555555555555", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"} {
		if err := validateTenantID(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	if err := validateCreate(CreateInput{
		Name: "x", Provider: ProviderOutlook, ClientID: "c", ClientSecret: "s", TenantID: "bad",
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateCreate(CreateInput{
		Name: "x", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		ServiceKey: `{"type":"service_account","client_email":"a@b.c","private_key":"k"}`,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEndpoints_tenant(t *testing.T) {
	a := &App{Provider: ProviderOutlook, TenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}
	auth, tok, _ := resolveEndpoints(a)
	if !strings.Contains(auth, "/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/") || !strings.Contains(tok, "/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/") {
		t.Fatalf("%s %s", auth, tok)
	}
	a2 := &App{Provider: ProviderOutlook}
	_, tok2, _ := resolveEndpoints(a2)
	if !strings.Contains(tok2, "/common/") {
		t.Fatal(tok2)
	}
}

func TestViewOf_w38Fields(t *testing.T) {
	v := ViewOf(&App{
		Provider: ProviderGmail, ServiceKey: crypto.EncryptedString{DEK: "d", CT: []byte{1}},
		TenantID: "common", AppOnly: true,
	})
	if !v.HasServiceKey || v.TenantID != "common" || !v.AppOnly {
		t.Fatalf("%+v", v)
	}
}

func TestClientCredentialsMaterial_TokenURL(t *testing.T) {
	m := ClientCredentialsMaterial{TenantID: "tid"}
	if m.TokenURL() != "https://login.microsoftonline.com/tid/oauth2/v2.0/token" {
		t.Fatal(m.TokenURL())
	}
}

type w38MemKeys struct {
	keys map[string]crypto.DataKey
}

func (m *w38MemKeys) Active(_ context.Context, orgID string) (*crypto.DataKey, error) {
	for i := range m.keys {
		k := m.keys[i]
		if k.Status != crypto.DEKActive {
			continue
		}
		if orgID == "" && k.OrganizationID == nil {
			cp := k
			return &cp, nil
		}
		if k.OrganizationID != nil && *k.OrganizationID == orgID {
			cp := k
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *w38MemKeys) Get(_ context.Context, id string) (*crypto.DataKey, error) {
	k, ok := m.keys[id]
	if !ok {
		return nil, crypto.ErrNotFound
	}
	cp := k
	return &cp, nil
}
func (m *w38MemKeys) Insert(_ context.Context, k crypto.DataKey) (*crypto.DataKey, error) {
	if m.keys == nil {
		m.keys = map[string]crypto.DataKey{}
	}
	m.keys[k.ID] = k
	cp := k
	return &cp, nil
}
func (m *w38MemKeys) All(context.Context) ([]crypto.DataKey, error) { return nil, nil }
func (m *w38MemKeys) UpdateWrap(context.Context, string, string, []byte) error {
	return nil
}
func (m *w38MemKeys) Retire(context.Context, string) error { return nil }

func w38Cipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = 3
	}
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: &w38MemKeys{keys: map[string]crypto.DataKey{}}, NewID: func() string {
		return store.NewID(store.PrefixDataKey)
	}, CacheTTL: time.Minute}
}

func TestServiceKeyEncryptRoundTrip(t *testing.T) {
	ctx := context.Background()
	cipher := w38Cipher(t)
	svc := &Service{Cipher: cipher}
	appID := store.NewID(store.PrefixOAuthApp)
	raw := `{"type":"service_account","client_email":"sa@x.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nM\n-----END PRIVATE KEY-----\n"}`
	enc, err := cipher.EncryptString(ctx, "org_w38", raw, crypto.AAD{
		Collection: CollectionApps, Field: "serviceKey", DocID: appID,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ID: appID, OrganizationID: "org_w38", ServiceKey: enc}
	got, err := svc.DecryptServiceKey(ctx, a)
	if err != nil || got != raw {
		t.Fatalf("%v %q", err, got)
	}
	if _, err := svc.DecryptServiceKey(ctx, &App{ID: appID}); errs.CodeOf(err) != errs.CodeOauthNotConnected {
		t.Fatal(err)
	}
	sec, err := cipher.EncryptString(ctx, "org_w38", "secret", crypto.AAD{
		Collection: CollectionApps, Field: "clientSecret", DocID: appID,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.ClientSecret = sec
	a.ClientID = "cid"
	a.AppOnly = true
	mat, err := svc.ClientCredentialsMaterial(ctx, a)
	if err != nil || mat.Secret != "secret" || mat.TenantID != "common" || !mat.AppOnly || mat.ClientID != "cid" {
		t.Fatalf("%+v %v", mat, err)
	}
	if !strings.Contains(mat.TokenURL(), "/common/") {
		t.Fatal(mat.TokenURL())
	}
}
