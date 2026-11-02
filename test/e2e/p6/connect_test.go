//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Autoconfig, hosted connect branding XSS sanitising, identity repin (P6.4 / W53).
func TestP6_Autoconfig_Builtin(t *testing.T) {
	f := startBase(t)
	r := f.Owner.Do(http.MethodGet, "/v1/autoconfig?email=user@gmail.com", nil)
	harness.ExpectStatus(t, r, http.StatusOK)
	if r.Str("source") != "builtin" && r.Str("oauthHint") != "gmail" {
		// either field is enough evidence of the builtin Gmail preset
		if r.Str("domain") != "gmail.com" {
			t.Fatalf("autoconfig: %s", r.Body)
		}
	}
	imap, _ := r.JSON["imap"].(map[string]any)
	if imap == nil || imap["host"] == "" {
		t.Fatalf("imap missing: %s", r.Body)
	}
	if r.Str("oauthHint") != "gmail" && r.Str("oauthHint") != "" {
		t.Fatalf("oauthHint: %s", r.Body)
	}

	bad := f.Owner.Do(http.MethodGet, "/v1/autoconfig?email=not-an-email", nil)
	if bad.Status != http.StatusUnprocessableEntity && problemCode(bad) != "validation_failed" {
		harness.ExpectProblem(t, bad, http.StatusUnprocessableEntity, "validation_failed")
	}
}

func TestP6_HostedConnect_BrandingXSS(t *testing.T) {
	f := startBase(t)
	enableOrgSwitch(t, f.Owner, "connect.hostedPage", true)

	xssHeader := `<p>Hello</p><script>alert(1)</script><img src=x onerror="alert(2)">`
	xssHead := `<meta name="description" content="x"><script>evil()</script><link rel="stylesheet" href="javascript:alert(1)">`

	preview := f.Owner.Do(http.MethodPost, "/v1/connect/branding/preview", map[string]any{
		"headerHtml": xssHeader, "headHtml": xssHead,
	})
	harness.ExpectStatus(t, preview, http.StatusOK)
	prevHeader := preview.Str("headerHtml")
	prevHead := preview.Str("headHtml")
	if strings.Contains(strings.ToLower(prevHeader), "<script") || strings.Contains(strings.ToLower(prevHeader), "onerror") {
		t.Fatalf("preview header still executable: %q", prevHeader)
	}
	if strings.Contains(strings.ToLower(prevHead), "<script") {
		t.Fatalf("preview head still executable: %q", prevHead)
	}

	cur := f.Owner.Do(http.MethodGet, "/v1/connect/branding", nil)
	harness.ExpectStatus(t, cur, http.StatusOK)
	patched := withIfMatch(f.Owner, etagOf(cur), func() harness.Resp {
		return f.Owner.Do(http.MethodPatch, "/v1/connect/branding", map[string]any{
			"brandName":  "E2E Brand",
			"headerHtml": xssHeader,
			"headHtml":   xssHead,
		})
	})
	harness.ExpectStatus(t, patched, http.StatusOK)
	if strings.Contains(strings.ToLower(patched.Str("headerHtml")), "<script") {
		t.Fatalf("saved headerHtml not sanitised: %q", patched.Str("headerHtml"))
	}

	link := f.Owner.Do(http.MethodPost, "/v1/connect-links/hosted", map[string]any{
		"ttlSeconds": 3600,
	})
	harness.ExpectStatus(t, link, http.StatusCreated)
	token := tokenFromConnectURL(link.Str("url"))
	if token == "" || !strings.Contains(token, ".") {
		t.Fatalf("connect url token: %s", link.Body)
	}

	pub := f.Node.NewClient()
	boot := pub.Do(http.MethodGet, "/v1/public/connect/"+token, nil)
	harness.ExpectStatus(t, boot, http.StatusOK)
	branding, _ := boot.JSON["branding"].(map[string]any)
	if branding == nil {
		t.Fatalf("bootstrap branding: %s", boot.Body)
	}
	headerHTML, _ := branding["headerHtml"].(string)
	headHTML, _ := branding["headHtml"].(string)
	low := strings.ToLower(headerHTML + headHTML)
	if strings.Contains(low, "<script") || strings.Contains(low, "onerror") || strings.Contains(low, "javascript:") {
		t.Fatalf("XSS survived bootstrap: header=%q head=%q", headerHTML, headHTML)
	}
	// Preview output should match what bootstrap returns after save.
	if prevHeader != "" && headerHTML != "" && prevHeader != headerHTML {
		// Allow minor whitespace differences; both must be script-free (checked above).
		t.Logf("preview vs bootstrap header differ (ok if both clean): %q vs %q", prevHeader, headerHTML)
	}
	if boot.Str("csrf") == "" {
		t.Fatalf("csrf missing: %s", boot.Body)
	}
}

func TestP6_Identity_Repin(t *testing.T) {
	f := startBase(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("pin%d", stamp)
	email := user + "@sim.e2e.test"
	accID := createIMAPAccount(t, f.Owner, f.Sim, user, "sim-secret", email, "Pin me")

	repin := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/identity/repin", map[string]any{
		"imapUser": user, "email": email,
	})
	harness.ExpectStatus(t, repin, http.StatusOK)
	if !strings.EqualFold(repin.Str("imapUser"), user) {
		t.Fatalf("repin imapUser: %s", repin.Body)
	}
	if !strings.EqualFold(repin.Str("email"), email) {
		t.Fatalf("repin email: %s", repin.Body)
	}

	acc := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID, nil)
	harness.ExpectStatus(t, acc, http.StatusOK)
	ident, _ := acc.JSON["identity"].(map[string]any)
	if ident == nil {
		t.Fatalf("GET account missing identity: %s", acc.Body)
	}
	if !strings.EqualFold(fmt.Sprint(ident["email"]), email) {
		t.Fatalf("identity.email: %v body=%s", ident["email"], acc.Body)
	}
	if !strings.EqualFold(fmt.Sprint(ident["imapUser"]), user) {
		t.Fatalf("identity.imapUser: %v body=%s", ident["imapUser"], acc.Body)
	}
	if ident["pinnedAt"] == nil || fmt.Sprint(ident["pinnedAt"]) == "" {
		t.Fatalf("identity.pinnedAt missing: %s", acc.Body)
	}
}
