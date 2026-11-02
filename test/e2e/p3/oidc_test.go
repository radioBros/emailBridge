//go:build e2e

package p3

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func TestOIDCLoginBrowserless(t *testing.T) {
	n := harness.StartNode(t, nil)
	owner, _ := n.OwnerSession(t)

	mock := newMockOIDC(t, "eb-e2e", "eb-e2e-secret")
	mock.Email = harness.OwnerEmail

	created := owner.Do(http.MethodPost, "/v1/oidc-providers", map[string]any{
		"issuerUrl":     mock.issuer,
		"clientId":      "eb-e2e",
		"clientSecret":  "eb-e2e-secret",
		"buttonLabel":   "E2E IdP",
		"autoProvision": false,
		"enabled":       true,
	})
	harness.ExpectStatus(t, created, http.StatusCreated)
	providerID, _ := created.JSON["id"].(string)
	if providerID == "" {
		t.Fatalf("provider id missing: %s", created.Body)
	}

	pub := owner.Do(http.MethodGet, "/v1/auth/oidc/providers", nil)
	harness.ExpectStatus(t, pub, http.StatusOK)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			// Stop on final app redirect (after callback), keep IdP + callback hops.
			if strings.HasPrefix(req.URL.String(), n.Base) && !strings.Contains(req.URL.Path, "/oidc/") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	start := n.Base + "/v1/auth/oidc/" + providerID + "/start?redirect_uri=" + url.QueryEscape(n.Base+"/")
	res, err := client.Get(start)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusFound || strings.Contains(loc, "sso_error") {
		t.Fatalf("oidc callback status %d loc %s", res.StatusCode, loc)
	}
	// Ensure session cookie landed (Jar stores Set-Cookie from callback before ErrUseLastResponse).
	var hasSession bool
	for _, ck := range jar.Cookies(mustParse(n.Base)) {
		if ck.Name == harness.SessionCookie {
			hasSession = true
		}
	}
	if !hasSession {
		for _, ck := range res.Cookies() {
			if ck.Name == harness.SessionCookie {
				hasSession = true
				jar.SetCookies(mustParse(n.Base), []*http.Cookie{ck})
			}
		}
	}
	if !hasSession {
		t.Fatalf("no session cookie after OIDC; set-cookie=%v loc=%s", res.Header.Values("Set-Cookie"), loc)
	}

	c := n.NewClient()
	c.C.Jar = jar
	me := c.Do(http.MethodGet, "/v1/me", nil)
	harness.ExpectStatus(t, me, http.StatusOK)
	if me.JSON["mfa"] != true {
		t.Fatalf("OIDC session should satisfy MFA: %s", me.Body)
	}
}

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
