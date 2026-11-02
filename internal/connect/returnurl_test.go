package connect

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestValidateReturnURLOpenRedirectGuards(t *testing.T) {
	pub := "https://gw.example.com"
	hosts := []string{"app.example.com"}
	ok := []struct {
		url  string
		prod bool
	}{
		{"https://gw.example.com/done", true},
		{"https://app.example.com/connected?x=1", true},
		{"https://APP.example.com/", true},
		{"http://localhost:5173/cb", false},
		{"https://127.0.0.1:8443/cb", false},
	}
	for _, c := range ok {
		if err := validateReturnURL(pub, c.url, hosts, c.prod); err != nil {
			t.Fatalf("%s (prod=%v): %v", c.url, c.prod, err)
		}
	}
	bad := []struct {
		url  string
		prod bool
	}{
		{"http://app.example.com/", true},            // https only
		{"http://localhost:5173/cb", true},           // localhost only outside production
		{"https://evil.example.com/", true},          // not listed
		{"https://app.example.com.evil.test/", true}, // suffix trick
		{"https://evilapp.example.com/", true},       // exact match only
		{"https://gw.example.com@evil.test/", true},  // credentials
		{"https://user:pw@app.example.com/", true},
		{"//evil.test/x", true},
		{"javascript:alert(1)", true},
		{"/relative", true},
		{"https://", true},
	}
	for _, c := range bad {
		if err := validateReturnURL(pub, c.url, hosts, c.prod); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%s (prod=%v) must be rejected: %v", c.url, c.prod, err)
		}
	}
	// No public URL and no list: nothing outside localhost is allowed (no fail-open).
	if err := validateReturnURL("", "https://anything.example/", nil, true); err == nil {
		t.Fatal("empty allow-list must reject")
	}
}

func TestNormalizeReturnHosts(t *testing.T) {
	got, err := normalizeReturnHosts([]string{" App.Example.com ", "app.example.com", "", "crm.acme.it"})
	if err != nil || strings.Join(got, ",") != "app.example.com,crm.acme.it" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"https://app.example.com", "app.example.com:443", "app.example.com/x", "*.example.com",
		"localhost", "-bad.example.com", "a..b", "exa mple.com", strings.Repeat("a", 250) + ".com"} {
		if _, err := normalizeReturnHosts([]string{bad}); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%q must be rejected: %v", bad, err)
		}
	}
	many := make([]string, maxReturnHosts+1)
	for i := range many {
		many[i] = "h.example.com"
	}
	if _, err := normalizeReturnHosts(many); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("too many")
	}
}

func TestOrgReturnHostsAllowListPerOrganisation(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "Acme", Version: 1}
	st.orgs["org_2"] = &orgDoc{ID: "org_2", Name: "Other", Version: 1}
	s := NewService(st, nil)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	admin := func(org string) context.Context {
		return tenancy.WithCaller(context.Background(), tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
		})
	}
	ctx1, ctx2 := admin("org_1"), admin("org_2")

	if _, err := s.CreateHosted(ctx1, HostedCreate{ReturnURL: "https://app.acme.it/done"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unlisted host: %v", err)
	}
	bad := []string{"https://app.acme.it"}
	if _, _, err := s.UpdateBranding(ctx1, BrandingUpdate{ReturnURLHosts: &bad}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("PATCH validates hosts: %v", err)
	}
	hosts := []string{"App.Acme.it"}
	b, _, err := s.UpdateBranding(ctx1, BrandingUpdate{ReturnURLHosts: &hosts}, "")
	if err != nil || len(b.ReturnURLHosts) != 1 || b.ReturnURLHosts[0] != "app.acme.it" {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := s.CreateHosted(ctx1, HostedCreate{ReturnURL: "https://app.acme.it/done"}); err != nil {
		t.Fatalf("listed host: %v", err)
	}
	// Another organisation's allow-list never applies.
	if _, err := s.CreateHosted(ctx2, HostedCreate{ReturnURL: "https://app.acme.it/done"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("cross-tenant allow-list: %v", err)
	}
	// The public bootstrap does not expose the list.
	token, link := putLink(st, "https://app.acme.it/done")
	_ = link
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil || boot.Branding.ReturnURLHosts != nil {
		t.Fatalf("bootstrap leaks hosts: %+v %v", boot.Branding, err)
	}
	empty := []string{}
	b, _, err = s.UpdateBranding(ctx1, BrandingUpdate{ReturnURLHosts: &empty}, "")
	if err != nil || len(b.ReturnURLHosts) != 0 {
		t.Fatalf("clear: %+v %v", b, err)
	}
	if stringList([]any{"a.example.com", 3}) == nil || stringList(nil) != nil {
		t.Fatal("stringList")
	}
}
