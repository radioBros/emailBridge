//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	local, err := crypto.NewLocal([]crypto.LocalKey{{ID: "t", Key: bytes.Repeat([]byte{9}, 32)}}, "t")
	if err != nil {
		t.Fatal(err)
	}
	ks := &crypto.MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(context.Background(), crypto.DataKeysCollection, ks.Indexes()...); err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: local, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
}

func oidcHarness(t *testing.T) (*harness, *mockOIDC, *OIDCProvider) {
	t.Helper()
	h := newHarness(t, nil)
	if err := EnsureOIDCIndexes(context.Background(), h.db); err != nil {
		t.Fatal(err)
	}
	h.svc.Cipher = testCipher(t, h.db)
	// httptest IdP is on loopback; allow it under the SSRF dialer (W35).
	h.svc.Egress = egress.NewDialer(egress.Policy{
		BlockPrivate: true, BlockLinkLocal: true,
		Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	})
	mock := newMockOIDC(t, "eb-client", "eb-secret")
	p, err := h.svc.createOIDCProvider(context.Background(), oidcCreateInput{
		IssuerURL: mock.issuer, ClientID: "eb-client", ClientSecret: "eb-secret",
		ButtonLabel: "Sign in with Test IdP", AutoProvision: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, mock, p
}

func followOIDC(t *testing.T, h *harness, startURL string, mock *mockOIDC) *http.Response {
	t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(startURL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound {
		return res
	}
	idp := res.Header.Get("Location")
	_ = res.Body.Close()
	if !strings.HasPrefix(idp, mock.issuer) {
		// Already a failure redirect from start
		res, err = noFollow.Get(idp)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	u, err := url.Parse(idp)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("login_hint", "sso@example.test")
	u.RawQuery = q.Encode()
	res, err = noFollow.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound {
		return res
	}
	callback := res.Header.Get("Location")
	_ = res.Body.Close()
	res, err = noFollow.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIntegrationOIDCSuccess(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")

	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/")
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusFound || strings.Contains(loc, "sso_error") {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d loc %s body %s", res.StatusCode, loc, body)
	}
	c := h.client()
	var gotSession bool
	for _, ck := range res.Cookies() {
		if ck.Name == SessionCookie {
			gotSession = true
			c.c.Jar.SetCookies(mustURL(h.base), []*http.Cookie{ck})
		}
	}
	if !gotSession {
		t.Fatalf("no session cookie; set-cookie=%v loc=%s", res.Header.Values("Set-Cookie"), loc)
	}
	me := c.me()
	if me.Email != "sso@example.test" || me.Method != MethodOIDC || !me.MFA {
		t.Fatalf("me = %+v", me)
	}
}

func TestIntegrationOIDCUnknownUser(t *testing.T) {
	h, mock, p := oidcHarness(t)
	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/login")
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	loc := res.Header.Get("Location")
	if !strings.Contains(loc, "sso_error=1") {
		t.Fatalf("want sso_error redirect, got %s status %d", loc, res.StatusCode)
	}
}

func TestIntegrationOIDCDomainDenied(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	dom := []string{"allowed.test"}
	_, err := h.svc.patchOIDCProvider(context.Background(), p.ID, oidcPatchInput{AllowedEmailDomains: &dom})
	if err != nil {
		t.Fatal(err)
	}
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want domain deny, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCBadSignature(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	mock.BadSig = true
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want bad sig fail, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCWrongAudience(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	mock.WrongAud = true
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want wrong aud fail, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCExpired(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	mock.Expired = true
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want expired fail, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCEmailNotVerified(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	f := false
	mock.EmailVerified = &f
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want unverified fail, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCWrongNonce(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	mock.WrongNonce = true
	start := h.base + BasePath + "/oidc/" + p.ID + "/start"
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want nonce fail, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCWrongState(t *testing.T) {
	h, _, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(h.base + BasePath + "/oidc/" + p.ID + "/callback?code=x&state=bogus")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("status %d loc %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCAutoProvision(t *testing.T) {
	h, mock, p := oidcHarness(t)
	on := true
	_, err := h.svc.patchOIDCProvider(context.Background(), p.ID, oidcPatchInput{AutoProvision: &on})
	if err != nil {
		t.Fatal(err)
	}
	hooked := false
	h.svc.ProvisionHook = func(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error {
		hooked = true
		return nil
	}
	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/")
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !hooked {
		t.Fatal("provision hook not called")
	}
	id, err := h.svc.findLimenUserByEmail(context.Background(), "sso@example.test")
	if err != nil || id == "" {
		t.Fatalf("user not created: %v %s", err, id)
	}
}

func TestIntegrationOIDCAdminCRUD(t *testing.T) {
	h, mock, _ := oidcHarness(t)
	h.svc.Audit = &audit.Writer{DB: h.db}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", InstanceOwner: true, Role: tenancy.RoleOwner,
		OrganizationID: store.NewID(store.PrefixOrganization), // provider changes are instance-wide: no org on the record
	})
	list, err := h.svc.adminListOIDC(ctx, nil)
	if err != nil || len(list.Body.Data) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	p2, err := h.svc.adminCreateOIDC(ctx, &oidcCreateIn{Body: oidcCreateBody{
		IssuerURL: mock.issuer + "/other", ClientID: "c2", ClientSecret: "s2", ButtonLabel: "Other",
	}})
	if err != nil {
		t.Fatal(err)
	}
	label := "Renamed"
	_, err = h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p2.Body.ID, Body: oidcPatchBody{ButtonLabel: &label}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.adminGetOIDC(ctx, &oidcIDIn{ProviderID: p2.Body.ID})
	if err != nil || got.Body.ButtonLabel != "Renamed" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := h.svc.adminDeleteOIDC(ctx, &oidcIDIn{ProviderID: p2.Body.ID}); err != nil {
		t.Fatal(err)
	}
	pub, err := h.svc.listPublicOIDCProviders(context.Background())
	if err != nil || len(pub) != 1 {
		t.Fatalf("public: %+v %v", pub, err)
	}
	_ = time.Second

	// Every admin change is audited, instance-wide, and never stores the client secret.
	cur, err := h.db.Collection("auditLog").Find(context.Background(), bson.M{"target.id": p2.Body.ID},
		options.Find().SetSort(bson.D{{Key: "at", Value: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	var recs []bson.M
	if err := cur.All(context.Background(), &recs); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range recs {
		actions = append(actions, r["action"].(string))
		if r["organizationId"] != nil {
			t.Fatalf("provider audit carries an organisation: %v", r)
		}
	}
	if strings.Join(actions, ",") != "oidc.provider.created,oidc.provider.updated,oidc.provider.deleted" {
		t.Fatalf("audit actions: %v", actions)
	}
	raw, err := json.Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"s2"`) || !strings.Contains(string(raw), `"[redacted]"`) {
		t.Fatalf("client secret in audit: %s", raw)
	}
	if !strings.Contains(string(raw), `"Renamed"`) {
		t.Fatalf("patch diff missing: %s", raw)
	}
}

func TestIntegrationOIDCPublicList(t *testing.T) {
	h, _, p := oidcHarness(t)
	res, err := http.Get(h.base + BasePath + "/oidc/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []OIDCPublicProvider `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if len(body.Data) != 1 || body.Data[0].ID != p.ID {
		t.Fatalf("%+v", body)
	}
}

func TestIntegrationOIDCInviteAllowsSSO(t *testing.T) {
	h, mock, p := oidcHarness(t)
	h.svc.InviteChecker = func(ctx context.Context, email string) (bool, error) {
		return email == "sso@example.test", nil
	}
	hooked := false
	h.svc.ProvisionHook = func(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error {
		hooked = true
		if providerID != p.ID {
			t.Fatalf("provider %s", providerID)
		}
		return nil
	}
	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/")
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound || strings.Contains(res.Header.Get("Location"), "sso_error") {
		t.Fatalf("invited SSO failed: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if !hooked {
		t.Fatal("provision hook")
	}
	id, err := h.svc.findLimenUserByEmail(context.Background(), "sso@example.test")
	if err != nil || id == "" {
		t.Fatalf("user: %v %s", err, id)
	}
}

func TestIntegrationOIDCUninvitedDenied(t *testing.T) {
	h, mock, p := oidcHarness(t)
	h.svc.InviteChecker = func(ctx context.Context, email string) (bool, error) {
		return false, nil
	}
	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/")
	res := followOIDC(t, h, start, mock)
	defer func() { _ = res.Body.Close() }()
	if !strings.Contains(res.Header.Get("Location"), "sso_error=1") {
		t.Fatalf("want deny, got %s", res.Header.Get("Location"))
	}
}

func TestIntegrationOIDCDisabledAndBadRedirect(t *testing.T) {
	h, _, p := oidcHarness(t)
	off := false
	if _, err := h.svc.patchOIDCProvider(context.Background(), p.ID, oidcPatchInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(h.base + BasePath + "/oidc/" + p.ID + "/start")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusUnauthorized {
		// writeErr may use problem status
		if res.StatusCode < 400 {
			t.Fatalf("disabled start status %d", res.StatusCode)
		}
	}
	on := true
	if _, err := h.svc.patchOIDCProvider(context.Background(), p.ID, oidcPatchInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	res, err = client.Get(h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape("https://evil.test/"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode < 400 {
		t.Fatalf("evil redirect status %d", res.StatusCode)
	}
}

func TestIntegrationOIDCGroupRolesCRUD(t *testing.T) {
	h, mock, _ := oidcHarness(t)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", InstanceOwner: true, Role: tenancy.RoleOwner,
	})
	org := store.NewID(store.PrefixOrganization)
	p, err := h.svc.adminCreateOIDC(ctx, &oidcCreateIn{Body: oidcCreateBody{
		IssuerURL: mock.issuer + "/gr", ClientID: "cg", ClientSecret: "sg", ButtonLabel: "G",
		GroupClaim: "groups", GroupRoles: map[string]string{"admins": org + ":admin"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.GroupRoles["admins"] != org+":admin" {
		t.Fatalf("%+v", p.Body.GroupRoles)
	}
	roles := map[string]string{"ops": org + ":operator"}
	patched, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.Body.ID, Body: oidcPatchBody{GroupRoles: &roles}})
	if err != nil || patched.Body.GroupRoles["ops"] != org+":operator" {
		t.Fatalf("%+v %v", patched, err)
	}
	bad := map[string]string{"x": "viewer"}
	if _, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.Body.ID, Body: oidcPatchBody{GroupRoles: &bad}}); err == nil {
		t.Fatal("bad groupRoles accepted")
	}
	if _, err := h.svc.adminDeleteOIDC(ctx, &oidcIDIn{ProviderID: p.Body.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationOIDCJSONFail(t *testing.T) {
	h, _, p := oidcHarness(t)
	req, _ := http.NewRequest(http.MethodGet, h.base+BasePath+"/oidc/"+p.ID+"/callback?code=x&state=bogus", nil)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusFound {
		// failOIDC with JSON returns 401
		if res.StatusCode < 400 {
			t.Fatalf("status %d", res.StatusCode)
		}
	}
}

func TestIntegrationOIDCPatchAllAndForbidden(t *testing.T) {
	h, mock, p := oidcHarness(t)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", InstanceOwner: true, Role: tenancy.RoleOwner,
	})
	org := store.NewID(store.PrefixOrganization)
	iss := mock.issuer + "/patched"
	cid, sec, label := "cid2", "sec2", "L2"
	scopes := []string{"openid", "email"}
	domains := []string{"Example.TEST"}
	claim := "groups"
	roles := map[string]string{"admins": org + ":owner"}
	ap, en := true, true
	got, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.ID, Body: oidcPatchBody{
		IssuerURL: &iss, ClientID: &cid, ClientSecret: &sec, Scopes: &scopes, ButtonLabel: &label,
		AllowedEmailDomains: &domains, GroupClaim: &claim, GroupRoles: &roles, AutoProvision: &ap, Enabled: &en,
	}})
	if err != nil || got.Body.ClientID != "cid2" || got.Body.GroupClaim != "groups" || got.Body.GroupRoles["admins"] != org+":owner" {
		t.Fatalf("%+v %v", got, err)
	}
	empty := ""
	if _, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.ID, Body: oidcPatchBody{ButtonLabel: &empty}}); err == nil {
		t.Fatal("empty label")
	}
	if _, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.ID, Body: oidcPatchBody{ClientID: &empty}}); err == nil {
		t.Fatal("empty client")
	}
	if _, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.ID, Body: oidcPatchBody{ClientSecret: &empty}}); err == nil {
		t.Fatal("empty secret")
	}
	if _, err := h.svc.adminPatchOIDC(ctx, &oidcPatchIn{ProviderID: p.ID, Body: oidcPatchBody{IssuerURL: &empty}}); err == nil {
		t.Fatal("empty issuer")
	}
	if _, err := h.svc.adminCreateOIDC(context.Background(), &oidcCreateIn{Body: oidcCreateBody{
		IssuerURL: mock.issuer + "/x", ClientID: "x", ClientSecret: "x", ButtonLabel: "x",
	}}); err == nil {
		t.Fatal("forbidden create")
	}
	if _, err := h.svc.adminGetOIDC(context.Background(), &oidcIDIn{ProviderID: p.ID}); err == nil {
		t.Fatal("forbidden get")
	}
	if _, err := h.svc.adminListOIDC(context.Background(), nil); err == nil {
		t.Fatal("forbidden list")
	}
	if _, err := h.svc.adminDeleteOIDC(context.Background(), &oidcIDIn{ProviderID: p.ID}); err == nil {
		t.Fatal("forbidden delete")
	}
	if _, err := h.svc.CreateOIDCUser(context.Background(), "not-an-email"); err == nil {
		t.Fatal("bad email")
	}
	h.svc.InviteChecker = func(ctx context.Context, email string) (bool, error) {
		return false, errors.New("invite boom")
	}
	_, err = h.svc.resolveOIDCUser(context.Background(), p, "new@example.test", nil)
	if err == nil {
		t.Fatal("invite checker err")
	}
}

func TestIntegrationOIDCReloginProvision(t *testing.T) {
	h, mock, p := oidcHarness(t)
	_ = h.user("sso@example.test")
	org := store.NewID(store.PrefixOrganization)
	claim := "groups"
	roles := map[string]string{"admins": org + ":admin"}
	_, err := h.svc.patchOIDCProvider(context.Background(), p.ID, oidcPatchInput{GroupClaim: &claim, GroupRoles: &roles})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	h.svc.ProvisionHook = func(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error {
		called++
		return nil
	}
	start := h.base + BasePath + "/oidc/" + p.ID + "/start?redirect_uri=" + url.QueryEscape(h.base+"/")
	res := followOIDC(t, h, start, mock)
	_ = res.Body.Close()
	if called == 0 {
		t.Fatal("provision on existing user")
	}
	// unknown path on handleOIDC
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := client.Get(h.base + BasePath + "/oidc/nope")
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode < 400 {
		t.Fatalf("status %d", r2.StatusCode)
	}
}
