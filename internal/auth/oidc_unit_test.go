package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestNormalizeGroupRoles(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	m, err := normalizeGroupRoles(map[string]string{"admins": org + ":owner", " ": "x"})
	if err != nil || m["admins"] != org+":owner" || len(m) != 1 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := normalizeGroupRoles(map[string]string{"g": "owner"}); err == nil {
		t.Fatal("plain role rejected")
	}
	if _, err := normalizeGroupRoles(map[string]string{"g": org + ":boss"}); err == nil {
		t.Fatal("bad role")
	}
	if m, err := normalizeGroupRoles(nil); err != nil || m != nil {
		t.Fatal(m, err)
	}
	if m, err := normalizeGroupRoles(map[string]string{" ": " "}); err != nil || m != nil {
		t.Fatal(m, err)
	}
}

func TestValidateOIDCCreate(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	if err := validateOIDCCreate(oidcCreateInput{}); err == nil {
		t.Fatal("empty")
	}
	base := oidcCreateInput{IssuerURL: "https://idp/", ClientID: "c", ClientSecret: "s", ButtonLabel: "B"}
	for _, bad := range []oidcCreateInput{
		{ClientID: "c", ClientSecret: "s", ButtonLabel: "B"},
		{IssuerURL: "https://idp/", ClientSecret: "s", ButtonLabel: "B"},
		{IssuerURL: "https://idp/", ClientID: "c", ButtonLabel: "B"},
		{IssuerURL: "https://idp/", ClientID: "c", ClientSecret: "s"},
	} {
		if err := validateOIDCCreate(bad); err == nil {
			t.Fatalf("want err for %+v", bad)
		}
	}
	if err := validateOIDCCreate(base); err != nil {
		t.Fatal(err)
	}
	base.GroupRoles = map[string]string{"g": "bad"}
	if err := validateOIDCCreate(base); err == nil {
		t.Fatal("bad roles")
	}
	base.GroupRoles = map[string]string{"g": org + ":viewer"}
	if err := validateOIDCCreate(base); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeDomainsAndEmailDomain(t *testing.T) {
	if d := normalizeDomains([]string{" Ex.COM ", "ex.com", ""}); len(d) != 1 || d[0] != "ex.com" {
		t.Fatalf("%v", d)
	}
	if normalizeDomains(nil) != nil {
		t.Fatal("nil")
	}
	if emailDomain("a@B.C") != "b.c" || emailDomain("nope") != "" {
		t.Fatal("emailDomain")
	}
}

func TestWantsJSONAndCallbackURL(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "application/json")
	if !wantsJSON(r) {
		t.Fatal("json")
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	if wantsJSON(r2) {
		t.Fatal("html")
	}
	s := &Service{o: Options{PublicURL: "https://eb.example/"}}
	if u := s.oidcCallbackURL("oidc_1"); u != "https://eb.example"+BasePath+"/oidc/oidc_1/callback" {
		t.Fatalf("%s", u)
	}
}

func TestGroupsFromClaimsMore(t *testing.T) {
	c := &oidcClaims{Raw: map[string]any{"g": "one", "arr": []string{"a", "b"}, "obj": map[string]any{"x": 1}}}
	if g := groupsFromClaims(c, "g"); len(g) != 1 || g[0] != "one" {
		t.Fatalf("%v", g)
	}
	if g := groupsFromClaims(c, "arr"); len(g) != 2 {
		t.Fatalf("%v", g)
	}
	if groupsFromClaims(c, "obj") != nil {
		t.Fatal("obj")
	}
	if groupsFromClaims(nil, "g") != nil {
		t.Fatal("nil claims")
	}
}

func TestIDTokenFromOauthToken(t *testing.T) {
	tok := &oauth2.Token{}
	tok = tok.WithExtra(map[string]any{"id_token": "abc"})
	raw, err := idTokenFromOauthToken(tok)
	if err != nil || raw != "abc" {
		t.Fatalf("%q %v", raw, err)
	}
	if _, err := idTokenFromOauthToken(&oauth2.Token{}); err == nil {
		t.Fatal("missing")
	}
}

func TestOIDCHelpersAndFailJSON(t *testing.T) {
	if stringsTrimSlash(" https://x/ ") != "https://x" {
		t.Fatal("trim slash")
	}
	s := &Service{o: Options{PublicURL: "https://eb.example"}, log: discard(), security: discard()}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "application/json")
	s.failOIDC(context.Background(), w, r, "https://eb.example/", "test")
	if w.Code < 400 {
		t.Fatalf("json fail %d", w.Code)
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	s.failOIDC(context.Background(), w2, r2, "https://eb.example/app?x=1", "test")
	if w2.Code != http.StatusFound || !strings.Contains(w2.Header().Get("Location"), "sso_error=1") {
		t.Fatalf("redirect fail %d %s", w2.Code, w2.Header().Get("Location"))
	}
	w3 := httptest.NewRecorder()
	s.failOIDC(context.Background(), w3, r2, "https://eb.example/", "root")
	if !strings.Contains(w3.Header().Get("Location"), "/login?sso_error=1") {
		t.Fatalf("login redirect %s", w3.Header().Get("Location"))
	}
	mig := Migration(25)
	if mig.Number != 25 || mig.Name == "" || mig.Up == nil {
		t.Fatalf("%+v", mig)
	}
}

func TestDomainAllowed(t *testing.T) {
	if !domainAllowed("a@Ex.Com", nil) {
		t.Fatal("empty allowlist")
	}
	if !domainAllowed("a@ex.com", []string{"ex.com"}) {
		t.Fatal("match")
	}
	if domainAllowed("a@other.com", []string{"ex.com"}) {
		t.Fatal("deny")
	}
}

func TestDefaultOIDCScopes(t *testing.T) {
	s := defaultOIDCScopes(nil)
	if len(s) != 3 || s[0] != "openid" {
		t.Fatalf("%v", s)
	}
	s = defaultOIDCScopes([]string{"email", "openid"})
	if len(s) != 2 {
		t.Fatalf("%v", s)
	}
}

func TestPKCEChallengeStable(t *testing.T) {
	a := oauth2.S256ChallengeFromVerifier("verifier-value-here")
	b := oauth2.S256ChallengeFromVerifier("verifier-value-here")
	if a == "" || a != b {
		t.Fatalf("%q %q", a, b)
	}
}

func TestGroupsFromClaims(t *testing.T) {
	c := &oidcClaims{Raw: map[string]any{"groups": []any{"admins", "devs"}}}
	g := groupsFromClaims(c, "groups")
	if len(g) != 2 || g[0] != "admins" {
		t.Fatalf("%v", g)
	}
	if groupsFromClaims(c, "") != nil {
		t.Fatal("empty claim")
	}
}

func TestMFAMethodOIDC(t *testing.T) {
	if !mfaMethod(MethodOIDC) {
		t.Fatal("oidc should satisfy MFA")
	}
}

func TestTrustedRedirect(t *testing.T) {
	s := &Service{o: Options{PublicURL: "https://eb.example", CORSOrigins: []string{"https://admin.example"}}}
	if u, ok := s.trustedRedirect("/dashboard"); !ok || u != "https://eb.example/dashboard" {
		t.Fatalf("%q %v", u, ok)
	}
	if _, ok := s.trustedRedirect("https://evil.test/"); ok {
		t.Fatal("evil")
	}
	if u, ok := s.trustedRedirect("https://admin.example/x"); !ok || u != "https://admin.example/x" {
		t.Fatalf("%q %v", u, ok)
	}
	if _, ok := s.trustedRedirect("//evil.test"); ok {
		t.Fatal("protocol-relative")
	}
}
