//go:build e2e

package p0

import (
	"net/http"
	"testing"
)

func TestLoginFlowsPasswordTOTPPasskeyLockout(t *testing.T) {
	n := StartNode(t, nil)
	c := n.Client()

	// password → MFA enrolment required
	r := c.signIn(ownerEmail, ownerPassword)
	expectStatus(t, r, http.StatusOK)
	expectProblem(t, c.do(http.MethodGet, "/v1/settings", nil), http.StatusUnauthorized, "mfa_required")
	me := c.do(http.MethodGet, "/v1/me", nil)
	expectStatus(t, me, http.StatusOK)
	if me.JSON["mfa"] == true {
		t.Fatal("password-only session must not be MFA yet")
	}

	secret, enrolCode := c.enrolTOTP(ownerPassword)
	me = c.do(http.MethodGet, "/v1/me", nil)
	expectStatus(t, me, http.StatusOK)
	if me.JSON["mfa"] != true {
		t.Fatalf("GET /v1/me after TOTP enrol: %s", me.Body)
	}
	if r := c.do(http.MethodGet, "/v1/settings", nil); r.Status != http.StatusOK {
		t.Fatalf("settings after enrol = %d %s", r.Status, r.Body)
	}

	// sign out
	if r := c.do(http.MethodPost, "/v1/auth/signout", map[string]any{}); r.Status != http.StatusOK && r.Status != http.StatusNoContent {
		t.Fatalf("signout = %d %s", r.Status, r.Body)
	}
	expectProblem(t, c.do(http.MethodGet, "/v1/me", nil), http.StatusUnauthorized, "unauthenticated")

	// sign in again with password + TOTP
	c2 := n.Client()
	r = c2.signIn(ownerEmail, ownerPassword)
	if r.Status != http.StatusOK || r.JSON["two_factor_required"] != true || c2.cookie(challengeCookie) == nil {
		t.Fatalf("2fa sign-in = %d %s", r.Status, r.Body)
	}
	code := freshTOTP(t, secret, enrolCode)
	if r := c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"}); r.Status != http.StatusOK {
		t.Fatalf("verify = %d %s", r.Status, r.Body)
	}
	me = c2.do(http.MethodGet, "/v1/me", nil)
	if me.Status != http.StatusOK || me.JSON["mfa"] != true {
		t.Fatalf("me after totp = %d %s", me.Status, me.Body)
	}

	// reused TOTP code refused
	c3 := n.Client()
	c3.signIn(ownerEmail, ownerPassword)
	expectProblem(t, c3.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"}),
		http.StatusUnauthorized, "mfa_invalid")

	// passkey registration + sign-in
	d := newDevice(n.Base)
	if r := c2.registerPasskey(d); r.Status != http.StatusOK {
		t.Fatalf("finish-registration = %d %s", r.Status, r.Body)
	}
	c4 := n.Client()
	if r := c4.passkeySignIn(d); r.Status != http.StatusOK {
		t.Fatalf("passkey sign-in = %d %s", r.Status, r.Body)
	}
	me = c4.do(http.MethodGet, "/v1/me", nil)
	if me.Status != http.StatusOK || me.JSON["mfa"] != true {
		t.Fatalf("passkey me = %d %s", me.Status, me.Body)
	}

	// lockout after threshold (dedicated IP + email so shared rate limits stay clear)
	lockEmail := "lockout@e2e.test"
	// create via invitation accept is heavy; use wrong password against owner from a locked IP path:
	// lock the owner email from a unique IP, then confirm 423.
	wrong := map[string]any{"credential": ownerEmail, "password": "definitely wrong password!!"}
	lock := n.Client()
	lock.ip = "203.0.113.90"
	for i := 0; i < 5; i++ {
		expectProblem(t, lock.do(http.MethodPost, "/v1/auth/signin/credential", wrong), http.StatusUnauthorized, "invalid_credentials")
	}
	r = lock.signIn(ownerEmail, ownerPassword)
	expectProblem(t, r, http.StatusLocked, "account_locked")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("Retry-After missing on lockout")
	}
	_ = lockEmail
}
