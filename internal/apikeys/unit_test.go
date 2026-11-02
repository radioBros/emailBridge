package apikeys

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/store"
)

func mustAccID(t *testing.T) string {
	t.Helper()
	return store.NewID(store.PrefixAccount)
}

func TestValidateScopes(t *testing.T) {
	if err := validateScopes(nil); err == nil {
		t.Fatal("empty")
	}
	if err := validateScopes([]string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := validateScopes([]string{"accounts:read", "accounts:write"}); err != nil {
		t.Fatal(err)
	}
	if err := validateScopes([]string{"nope:read"}); err == nil {
		t.Fatal("unknown resource")
	}
	if err := validateScopes([]string{"accounts:admin"}); err == nil {
		t.Fatal("bad mode")
	}
	if err := validateScopes([]string{"accounts:read", "accounts:read"}); err == nil {
		t.Fatal("dup")
	}
	acc := mustAccID(t)
	if err := validateScopes([]string{"accounts:write:" + acc}); err != nil {
		t.Fatal(err)
	}
	if err := validateScopes([]string{"accounts:write:not-an-id"}); err == nil {
		t.Fatal("bad resource id")
	}
	if err := validateScopes([]string{"logs:read:" + acc}); err == nil {
		t.Fatal("logs is not granular")
	}
	if err := validateScopes([]string{"mcp:use"}); err != nil {
		t.Fatal(err)
	}
	if err := validateScopes([]string{"mcp:read"}); err != nil {
		t.Fatal(err)
	}
	if err := validateScopes([]string{"mcp:admin"}); err == nil {
		t.Fatal("bad mcp mode")
	}
}

func TestHoldsScopeAndSubset(t *testing.T) {
	held := []string{"accounts:write", "logs:read"}
	if !holdsScope(held, "accounts:read") || !holdsScope(held, "accounts:write") {
		t.Fatal("write implies read")
	}
	if holdsScope(held, "settings:read") {
		t.Fatal("missing")
	}
	if !holdsScope([]string{"admin"}, "accounts:write") {
		t.Fatal("admin")
	}
	if holdsScope([]string{"accounts:write"}, "admin") {
		t.Fatal("non-admin must not grant admin")
	}
	if holdsScope([]string{"accounts:read"}, "accounts:write") {
		t.Fatal("read must not grant write")
	}
	if holdsScope([]string{"webhooks:write"}, "accounts:read") {
		t.Fatal("other resource")
	}
	if err := scopesSubset(held, []string{"accounts:read", "logs:read"}); err != nil {
		t.Fatal(err)
	}
	if err := scopesSubset(held, []string{"settings:write"}); err == nil {
		t.Fatal("want scope_missing")
	}
	acc1 := mustAccID(t)
	acc2 := mustAccID(t)
	if !holdsScope([]string{"accounts:write"}, "accounts:write:"+acc1) {
		t.Fatal("org-wide may grant granular")
	}
	if holdsScope([]string{"accounts:write:" + acc1}, "accounts:write") {
		t.Fatal("granular must not grant org-wide")
	}
	if holdsScope([]string{"accounts:write:" + acc1}, "accounts:write:"+acc2) {
		t.Fatal("different id")
	}
	if !holdsScope([]string{"accounts:write:" + acc1}, "accounts:read:"+acc1) {
		t.Fatal("granular write implies read")
	}
	if err := scopesSubset([]string{"accounts:write:" + acc1}, []string{"accounts:read:" + acc1}); err != nil {
		t.Fatal(err)
	}
}

func TestGranularScopesMigration(t *testing.T) {
	m := GranularScopesMigration(22)
	if m.Number != 22 || m.Name == "" {
		t.Fatal(m)
	}
	if err := m.Up(nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictionsIPAndReferrer(t *testing.T) {
	if err := validateRestrictions(&Restrictions{IPs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("bad cidr")
	}
	if err := validateRestrictions(&Restrictions{IPs: []string{"127.0.0.1/32", "10.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	ref := &Restrictions{Referrers: []string{"https://app.example.com/*"}}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Referer", "https://app.example.com/x")
	if err := checkReferrer(req, ref); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Referer", "https://evil.test/")
	if err := checkReferrer(req, ref); err == nil {
		t.Fatal("want referrer denied")
	}
}

func TestCheckIPMappedAndBare(t *testing.T) {
	cidr4 := &Restrictions{IPs: []string{"203.0.113.0/24"}}
	bare4 := &Restrictions{IPs: []string{"203.0.113.7"}}
	cidr6 := &Restrictions{IPs: []string{"2001:db8::/32"}}
	mappedPrefix := &Restrictions{IPs: []string{"::ffff:203.0.113.0/120"}}
	cases := []struct {
		name string
		ip   netip.Addr
		r    *Restrictions
		deny bool
	}{
		{"plain v4 in cidr", netip.MustParseAddr("203.0.113.9"), cidr4, false},
		{"mapped v4 in cidr", netip.MustParseAddr("::ffff:203.0.113.7"), cidr4, false},
		{"mapped v4 outside", netip.MustParseAddr("::ffff:198.51.100.1"), cidr4, true},
		{"plain outside", netip.MustParseAddr("198.51.100.1"), cidr4, true},
		{"bare plain", netip.MustParseAddr("203.0.113.7"), bare4, false},
		{"bare mapped", netip.MustParseAddr("::ffff:203.0.113.7"), bare4, false},
		{"bare mismatch", netip.MustParseAddr("203.0.113.8"), bare4, true},
		{"ipv6 prefix", netip.MustParseAddr("2001:db8::1"), cidr6, false},
		{"ipv6 outside", netip.MustParseAddr("2001:db9::1"), cidr6, true},
		{"mapped-prefix rule", netip.MustParseAddr("203.0.113.7"), mappedPrefix, false},
		{"mapped-prefix mapped client", netip.MustParseAddr("::ffff:203.0.113.7"), mappedPrefix, false},
		{"nil restrictions", netip.MustParseAddr("1.2.3.4"), nil, false},
		{"empty ips", netip.MustParseAddr("1.2.3.4"), &Restrictions{}, false},
		{"invalid client", netip.Addr{}, cidr4, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkIP(c.ip, c.r)
			if c.deny && err == nil {
				t.Fatal("want deny")
			}
			if !c.deny && err != nil {
				t.Fatal(err)
			}
		})
	}
	p := unmapPrefix(netip.MustParsePrefix("203.0.113.0/24"))
	if !p.Addr().Is4() || p.Bits() != 24 {
		t.Fatal(p)
	}
}

func TestBearerTokenScheme(t *testing.T) {
	mk := func(h string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if h != "" {
			r.Header.Set("Authorization", h)
		}
		return r
	}
	if tok, ok := bearerToken(mk("")); ok || tok != "" {
		t.Fatal("absent")
	}
	if tok, ok := bearerToken(mk("Basic abc")); ok || tok != "" {
		t.Fatal("other scheme")
	}
	for _, h := range []string{"Bearer ebk_x", "bearer ebk_x", "BEARER ebk_x"} {
		tok, ok := bearerToken(mk(h))
		if !ok || tok != "ebk_x" {
			t.Fatalf("%q → %q %v", h, tok, ok)
		}
	}
	tok, ok := bearerToken(mk("Bearer "))
	if !ok || tok != "" {
		t.Fatalf("empty bearer: %q %v", tok, ok)
	}
}

func TestCursorAndSort(t *testing.T) {
	if _, _, err := parseSort("bogus"); err == nil {
		t.Fatal("sort")
	}
	f, d, err := parseSort("-createdAt")
	if err != nil || f != "createdAt" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	p := cursorPayload{ID: "key_01hxxxxxxxxxxxxxxxxxxxxxx", Field: "_id", Desc: true}
	s := encodeCursor(p)
	got, err := decodeCursor(s)
	if err != nil || got.ID != p.ID {
		t.Fatal(got, err)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("bad cursor")
	}
	kf, err := cursorFilter(cursorPayload{ID: "key_a"}, "_id", -1)
	if err != nil || kf["_id"] == nil {
		t.Fatal(kf, err)
	}
}

func TestETagAndHelpers(t *testing.T) {
	if formatETag(3) != `"v3"` || parseETag(`"v3"`) != 3 || parseETag("v3") != 3 {
		t.Fatal(formatETag(3), parseETag(`"v3"`))
	}
	if verifiedCacheKey("abc") != "limen:api-key:abc" {
		t.Fatal(verifiedCacheKey("abc"))
	}
	h := hashToken("ebk_test")
	if h == "" || h == hashToken("other") {
		t.Fatal(h)
	}
	if cloneRestrictions(nil) != nil || cloneRateLimit(nil) != nil || cloneTime(nil) != nil {
		t.Fatal("nil clones")
	}
	now := time.Now()
	if cloneTime(&now).Unix() != now.Unix() {
		t.Fatal("time")
	}
	r := cloneRestrictions(&Restrictions{IPs: []string{"1/1"}})
	r.IPs[0] = "x"
	if r.IPs[0] == "" {
		t.Fatal("clone")
	}
}

func TestFmtID(t *testing.T) {
	if fmtID("abc") != "abc" || fmtID(nil) != "<nil>" {
		t.Fatal(fmtID("abc"), fmtID(nil))
	}
}
