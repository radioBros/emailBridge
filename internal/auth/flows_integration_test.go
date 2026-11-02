//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func TestIntegrationPasswordSignInAndProblems(t *testing.T) {
	h := newHarness(t, nil)
	id := h.user("Admin@Example.test")
	c := h.client()

	r := c.signIn("admin@example.test")
	if r.status != http.StatusOK {
		t.Fatalf("sign-in = %d %s", r.status, r.body)
	}
	var sc *http.Cookie
	for _, ck := range (&http.Response{Header: r.header}).Cookies() {
		if ck.Name == SessionCookie && ck.Value != "" {
			sc = ck
		}
	}
	if sc == nil || !sc.HttpOnly || sc.SameSite != http.SameSiteStrictMode || sc.Path != "/" || sc.Domain != "" {
		t.Fatalf("session cookie = %+v", sc)
	}
	if sc.MaxAge < 43000 || sc.MaxAge > 43200 {
		t.Fatalf("cookie max-age = %d (want 12 h)", sc.MaxAge)
	}
	if sc.Secure {
		t.Fatal("Secure must be off only because EB_ENV=development")
	}
	me := c.me()
	if me.AuthUserID != id || me.Email != "admin@example.test" || me.Method != MethodPassword || me.MFA || me.SessionID == "" {
		t.Fatalf("identity = %+v", me)
	}
	// programmatic creation marks the address verified (SP3 finding 8)
	var u bson.M
	if err := h.db.Collection("auth_users").FindOne(context.Background(), bson.M{"_id": id}).Decode(&u); err != nil || u["email_verified_at"] == nil {
		t.Fatalf("email_verified_at = %v %v", u["email_verified_at"], err)
	}

	// error bodies are problems with our codes
	anon := h.client()
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{"credential": "admin@example.test", "password": "wrong password!!"}), 401, "invalid_credentials")
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/signup/credential", map[string]any{"email": "x@example.test", "password": testPassword}), 404, "not_found")
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/api-keys", map[string]any{"name": "k"}), 404, "not_found")
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{}), 422, "validation_failed")
	expectProblem(t, anon.do(http.MethodGet, "/v1/auth/me", nil), 401, "unauthenticated")
	expectProblem(t, anon.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
	foreign := h.client()
	foreign.hdr = map[string]string{"Origin": "https://evil.example"}
	expectProblem(t, foreign.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{"credential": "a@b.c", "password": "x"}), 403, "origin_rejected")
	plain := h.client()
	plain.hdr = map[string]string{"Content-Type": "text/plain", "X-Requested-With": ""}
	expectProblem(t, plain.do(http.MethodPost, "/v1/auth/signin/credential", "{}"), 403, "csrf_failed")
	// a successful Limen response passes through untouched
	if r := c.do(http.MethodGet, "/v1/auth/me", nil); r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /v1/auth/me = %d %s", r.status, r.body)
	}
	// password policy: too long a new password
	long := strings.Repeat("x", 129)
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/passwords/reset", map[string]any{"token": "t", "new_password": long}), 422, "validation_failed")

	// sign-out revokes the session
	if r := c.do(http.MethodPost, "/v1/auth/signout", map[string]any{}); r.status != http.StatusOK && r.status != http.StatusNoContent {
		t.Fatalf("signout = %d %s", r.status, r.body)
	}
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
}

func TestIntegrationMFAGateAndTOTP(t *testing.T) {
	h := newHarness(t, nil)
	h.user("owner@example.test")
	c := h.client()
	c.signIn("owner@example.test")

	// enrolment state: only the enrolment endpoints
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "mfa_required")
	expectProblem(t, c.do(http.MethodPost, "/v1/auth/revoke-sessions", nil), 401, "mfa_required")
	if c.me().MFA {
		t.Fatal("password-only session must not be MFA")
	}
	secret, enrolCode := c.enrolTOTP()
	// Limen rotated the session on enrolment; the new session is MFA
	if me := c.me(); !me.MFA || me.Method != MethodPasswordTOTP {
		t.Fatalf("after enrolment = %+v", me)
	}
	if r := c.do(http.MethodGet, "/v1/things", nil); r.status != http.StatusOK {
		t.Fatalf("things after enrolment = %d %s", r.status, r.body)
	}
	codesResp := c.do(http.MethodGet, "/v1/auth/two-factor/backup-codes", nil)
	var backup []string
	if err := json.Unmarshal(codesResp.body, &backup); err != nil || len(backup) == 0 {
		var wrapped struct {
			BackupCodes []string `json:"backup_codes"`
		}
		_ = json.Unmarshal(codesResp.body, &wrapped)
		backup = wrapped.BackupCodes
	}

	// password + TOTP sign-in
	c2 := h.client()
	r := c2.signIn("owner@example.test")
	if r.status != http.StatusOK || r.json["two_factor_required"] != true || c2.cookie(ChallengeCookie) == nil || c2.cookie(SessionCookie) != nil {
		t.Fatalf("2fa sign-in = %d %s", r.status, r.body)
	}
	expectProblem(t, c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": "000000", "method": "totp"}), 401, "mfa_invalid")
	code := freshTOTP(t, secret, enrolCode)
	if r := c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"}); r.status != http.StatusOK {
		t.Fatalf("verify = %d %s", r.status, r.body)
	}
	if me := c2.me(); !me.MFA || me.Method != MethodPasswordTOTP {
		t.Fatalf("totp session = %+v", me)
	}

	// backup code as the second factor
	if len(backup) > 0 {
		c3 := h.client()
		c3.signIn("owner@example.test")
		if r := c3.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": backup[0]}); r.status != http.StatusOK {
			t.Fatalf("backup verify = %d %s", r.status, r.body)
		}
		if me := c3.me(); me.Method != MethodPasswordBackup {
			t.Fatalf("backup session = %+v", me)
		}
	} else {
		t.Logf("backup codes not returned: %s", codesResp.body)
	}
	if ok, err := h.svc.HasMFA(context.Background(), c2.me().AuthUserID); err != nil || !ok {
		t.Fatalf("HasMFA = %v %v", ok, err)
	}
}

func TestIntegrationMFANotRequired(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.MFARequired = false })
	h.user("relaxed@example.test")
	c := h.client()
	c.signIn("relaxed@example.test")
	if r := c.do(http.MethodGet, "/v1/things", nil); r.status != http.StatusOK {
		t.Fatalf("things = %d %s", r.status, r.body)
	}
}

func TestIntegrationEmailOTP(t *testing.T) {
	h := newHarness(t, nil)
	h.user("otp@example.test")
	h.user("victim@example.test")
	c := h.client()
	c.signIn("otp@example.test")
	c.enrolTOTP() // email OTP needs TOTP enrolment in Limen; the code itself is unused here

	// no challenge cookie: refused, no mail
	anon := h.client()
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "otp@example.test"}), 401, "unauthenticated")
	if _, n := h.mailer.last(MailMFACode); n != 0 {
		t.Fatal("a code mail was sent without a challenge")
	}
	// a forged challenge cookie is refused too
	anon.c.Jar.SetCookies(mustURL(h.base), []*http.Cookie{{Name: ChallengeCookie, Value: "forged"}})
	expectProblem(t, anon.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "otp@example.test"}), 401, "unauthenticated")

	c2 := h.client()
	c2.signIn("otp@example.test")
	// the challenge of one account cannot mail another address
	expectProblem(t, c2.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "victim@example.test"}), 401, "unauthenticated")
	if r := c2.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "OTP@example.test"}); r.status != http.StatusOK {
		t.Fatalf("otp send = %d %s", r.status, r.body)
	}
	m, n := h.mailer.last(MailMFACode)
	if n != 1 || m.To != "otp@example.test" || len(m.Data["code"]) != 6 {
		t.Fatalf("mail = %+v (%d)", m, n)
	}
	if r := c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": m.Data["code"], "method": "otp"}); r.status != http.StatusOK {
		t.Fatalf("otp verify = %d %s", r.status, r.body)
	}
	if me := c2.me(); !me.MFA || me.Method != MethodPasswordOTP {
		t.Fatalf("otp session = %+v", me)
	}

	// at most 5 sends per 15 minutes per user
	c3 := h.client()
	c3.signIn("otp@example.test")
	for i := 0; i < 4; i++ {
		if r := c3.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "otp@example.test"}); r.status != http.StatusOK {
			t.Fatalf("send %d = %d %s", i+2, r.status, r.body)
		}
	}
	r := c3.do(http.MethodPost, "/v1/auth/two-factor/otp/send", map[string]any{"email": "otp@example.test"})
	expectProblem(t, r, 429, "rate_limited")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("Retry-After missing")
	}
	// a failing mail queue is logged, never surfaced to Limen
	h.mailer.err = errors.New("queue down")
	h.svc.sendPasswordReset("otp@example.test", "tok")
	if m, _ := h.mailer.last(MailPasswordReset); m.Data["token"] != "tok" {
		t.Fatalf("reset mail = %+v", m)
	}
}

func TestIntegrationPasskeys(t *testing.T) {
	h := newHarness(t, nil)
	id := h.user("pk@example.test")
	c := h.client()
	c.signIn("pk@example.test")
	if ok, _ := h.svc.HasMFA(context.Background(), id); ok {
		t.Fatal("no factor yet")
	}
	d := newDevice(h)
	// passkey registration is allowed in the enrolment state and upgrades the session
	if r := c.registerPasskey(d); r.status != http.StatusOK {
		t.Fatalf("finish-registration = %d %s", r.status, r.body)
	}
	if me := c.me(); !me.MFA || me.Method != MethodPasswordPasskey {
		t.Fatalf("after registration = %+v", me)
	}
	if ok, _ := h.svc.HasMFA(context.Background(), id); !ok {
		t.Fatal("HasMFA after passkey")
	}

	// discoverable passkey sign-in with user verification
	c2 := h.client()
	if r := c2.passkeySignIn(d); r.status != http.StatusOK {
		t.Fatalf("finish-authentication = %d %s", r.status, r.body)
	}
	if me := c2.me(); !me.MFA || me.Method != MethodPasskey || me.AuthUserID != id {
		t.Fatalf("passkey session = %+v", me)
	}
	if r := c2.do(http.MethodGet, "/v1/things", nil); r.status != http.StatusOK {
		t.Fatalf("things = %d", r.status)
	}

	// an assertion without user verification is refused (our patched plugin, SP3 finding 4)
	nouv := *d
	nouv.auth.Options.UserNotVerified = true
	c3 := h.client()
	expectProblem(t, c3.passkeySignIn(&nouv), 401, "passkey_failed")
	if c3.cookie(SessionCookie) != nil {
		t.Fatal("no session for an unverified assertion")
	}

	// a password-only session of a passkey user is still not MFA
	c4 := h.client()
	c4.signIn("pk@example.test")
	expectProblem(t, c4.do(http.MethodGet, "/v1/things", nil), 401, "mfa_required")
	// … and a stolen password cannot enrol the attacker's own factor to lift that session to MFA
	expectProblem(t, c4.do(http.MethodGet, "/v1/auth/passkeys/begin-registration", nil), 401, "mfa_required")
	expectProblem(t, c4.do(http.MethodPost, "/v1/auth/two-factor/initiate-setup", map[string]any{"password": testPassword}), 401, "mfa_required")
	if me := c4.me(); me.MFA || me.Method != MethodPassword {
		t.Fatalf("password session of a passkey user = %+v", me)
	}
}

func TestIntegrationLockout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.LockoutThreshold = 3 })
	h.user("lock@example.test")
	h.user("other@example.test")
	wrong := map[string]any{"credential": "lock@example.test", "password": "definitely wrong"}

	c := h.client()
	c.ip = "203.0.113.10"
	for i := 0; i < 3; i++ {
		expectProblem(t, c.do(http.MethodPost, "/v1/auth/signin/credential", wrong), 401, "invalid_credentials")
	}
	// locked: even the right password is refused without calling Limen
	r := c.signIn("lock@example.test")
	expectProblem(t, r, 423, "account_locked")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("Retry-After missing")
	}
	// the email is locked from any IP …
	c2 := h.client()
	c2.ip = "203.0.113.20"
	expectProblem(t, c2.signIn("lock@example.test"), 423, "account_locked")
	// … and the IP is locked for any email
	expectProblem(t, c.signIn("other@example.test"), 423, "account_locked")

	// a success resets the email counter
	c3 := h.client()
	c3.ip = "203.0.113.30"
	bad := map[string]any{"credential": "other@example.test", "password": "definitely wrong"}
	for i := 0; i < 2; i++ {
		c3.ip = "203.0.113.3" + string(rune('1'+i))
		expectProblem(t, c3.do(http.MethodPost, "/v1/auth/signin/credential", bad), 401, "invalid_credentials")
	}
	c3.ip = "203.0.113.40"
	if r := c3.signIn("other@example.test"); r.status != http.StatusOK {
		t.Fatalf("sign-in = %d %s", r.status, r.body)
	}
	for i := 0; i < 2; i++ {
		c3.ip = "203.0.113.5" + string(rune('1'+i))
		expectProblem(t, c3.do(http.MethodPost, "/v1/auth/signin/credential", bad), 401, "invalid_credentials")
	}
	c3.ip = "203.0.113.60"
	if r := c3.signIn("other@example.test"); r.status != http.StatusOK {
		t.Fatalf("counter was not reset: %d %s", r.status, r.body)
	}
}

func TestIntegrationTOTPReplay(t *testing.T) {
	h := newHarness(t, nil)
	h.user("replay@example.test")
	c := h.client()
	c.signIn("replay@example.test")
	secret, enrolCode := c.enrolTOTP()

	codesResp := c.do(http.MethodGet, "/v1/auth/two-factor/backup-codes", nil)
	var backup []string
	if err := json.Unmarshal(codesResp.body, &backup); err != nil || len(backup) == 0 {
		var wrapped struct {
			BackupCodes []string `json:"backup_codes"`
		}
		_ = json.Unmarshal(codesResp.body, &wrapped)
		backup = wrapped.BackupCodes
	}

	code := freshTOTP(t, secret, enrolCode)
	c2 := h.client()
	c2.signIn("replay@example.test")
	if r := c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"}); r.status != http.StatusOK {
		t.Fatalf("first verify = %d %s", r.status, r.body)
	}
	c3 := h.client()
	c3.signIn("replay@example.test")
	expectProblem(t, c3.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"}), 401, "mfa_invalid")
	// whitespace around a used code must not mint a new claim key (Limen trims before verify)
	expectProblem(t, c3.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": " " + code + " ", "method": "totp"}), 401, "mfa_invalid")
	expectProblem(t, c3.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": " totp"}), 401, "mfa_invalid")

	fresh := freshTOTP(t, secret, enrolCode, code)
	c4 := h.client()
	c4.signIn("replay@example.test")
	if r := c4.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": fresh, "method": "totp"}); r.status != http.StatusOK {
		t.Fatalf("fresh verify = %d %s", r.status, r.body)
	}

	// backup codes are not claimed as TOTP and still work
	if len(backup) == 0 {
		t.Fatalf("backup codes missing: %s", codesResp.body)
	}
	c5 := h.client()
	c5.signIn("replay@example.test")
	if r := c5.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": backup[0]}); r.status != http.StatusOK {
		t.Fatalf("backup verify = %d %s", r.status, r.body)
	}
	// a second use of the same backup code fails at Limen (consumed), not via TOTP claim
	c6 := h.client()
	c6.signIn("replay@example.test")
	expectProblem(t, c6.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": backup[0]}), 401, "mfa_invalid")
}

func TestIntegrationMFALockout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.LockoutThreshold = 3 })
	h.user("mfalock@example.test")
	c := h.client()
	c.signIn("mfalock@example.test")
	secret, enrolCode := c.enrolTOTP()

	c2 := h.client()
	c2.signIn("mfalock@example.test")
	for i := 0; i < 3; i++ {
		expectProblem(t, c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": "000000", "method": "totp"}), 401, "mfa_invalid")
	}
	code := freshTOTP(t, secret, enrolCode)
	r := c2.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"})
	expectProblem(t, r, 423, "account_locked")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("Retry-After missing")
	}

	// success before the threshold resets the counter (fresh user: the locked one stays locked)
	h.user("mfaok@example.test")
	c4 := h.client()
	c4.signIn("mfaok@example.test")
	secret2, enrol2 := c4.enrolTOTP()
	c5 := h.client()
	c5.signIn("mfaok@example.test")
	for i := 0; i < 2; i++ {
		expectProblem(t, c5.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": "000000", "method": "totp"}), 401, "mfa_invalid")
	}
	code2 := freshTOTP(t, secret2, enrol2)
	if r := c5.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code2, "method": "totp"}); r.status != http.StatusOK {
		t.Fatalf("verify after 2 failures = %d %s", r.status, r.body)
	}
	c6 := h.client()
	c6.signIn("mfaok@example.test")
	for i := 0; i < 2; i++ {
		expectProblem(t, c6.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": "000000", "method": "totp"}), 401, "mfa_invalid")
	}
	code3 := freshTOTP(t, secret2, enrol2, code2)
	if r := c6.do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code3, "method": "totp"}); r.status != http.StatusOK {
		t.Fatalf("counter was not reset: %d %s", r.status, r.body)
	}
}

func TestIntegrationLoginRateLimit(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.LoginRate.Count = 2 })
	c := h.client()
	c.ip = "198.51.100.7"
	body := map[string]any{"credential": "nobody@example.test", "password": "whatever whatever"}
	c.do(http.MethodPost, "/v1/auth/signin/credential", body)
	c.do(http.MethodPost, "/v1/auth/signin/credential", body)
	r := c.do(http.MethodPost, "/v1/auth/signin/credential", body)
	expectProblem(t, r, 429, "rate_limited")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("Retry-After lost in translation")
	}
	// the limit is per trusted-proxy client IP, not per X-Forwarded-For value of anyone
	c.ip = "198.51.100.8"
	expectProblem(t, c.do(http.MethodPost, "/v1/auth/signin/credential", body), 401, "invalid_credentials")
}

func TestIntegrationSessionRefreshAndExpiry(t *testing.T) {
	h := newHarness(t, nil)
	h.user("slide@example.test")
	c := h.client()
	c.signIn("slide@example.test")
	token := c.cookie(SessionCookie).Value
	sessions := h.db.Collection("auth_sessions")
	ctx := context.Background()
	// make the session due for extension (last extended 20 min ago, update age is 15 min)
	due := time.Now().Add(12*time.Hour - 20*time.Minute)
	if _, err := sessions.UpdateOne(ctx, bson.M{"token": token}, bson.M{"$set": bson.M{"expires_at": due, "created_at": time.Now().Add(-20 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	r := c.do(http.MethodGet, "/v1/me", nil)
	var refreshed *http.Cookie
	for _, ck := range (&http.Response{Header: r.header}).Cookies() {
		if ck.Name == SessionCookie {
			refreshed = ck
		}
	}
	if r.status != http.StatusOK || refreshed == nil || refreshed.Value != token || refreshed.MaxAge < 43000 {
		t.Fatalf("refresh = %d %+v", r.status, refreshed)
	}
	var s bson.M
	_ = sessions.FindOne(ctx, bson.M{"token": token}).Decode(&s)
	if exp := s["expires_at"].(bson.DateTime).Time(); exp.Before(due.Add(10 * time.Minute)) {
		t.Fatalf("expires_at not extended: %v", exp)
	}
	if s[methodField] != MethodPassword {
		t.Fatalf("method survived the extension? %v", s[methodField])
	}

	// an expired session → 401 session_expired, and the stale cookie is cleared
	if _, err := sessions.UpdateOne(ctx, bson.M{"token": token}, bson.M{"$set": bson.M{"expires_at": time.Now().Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	r = c.do(http.MethodGet, "/v1/me", nil)
	expectProblem(t, r, 401, "session_expired")
	if c.cookie(SessionCookie) != nil {
		t.Fatal("expired cookie not cleared")
	}
	// an unknown token is simply unauthenticated
	c.c.Jar.SetCookies(mustURL(h.base), []*http.Cookie{{Name: SessionCookie, Value: "nope", Path: "/"}})
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
}

func TestIntegrationUserAPI(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	if _, err := h.svc.CreateUser(ctx, "x@example.test", "short"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("short password = %v", err)
	}
	if _, err := h.svc.CreateUser(ctx, "not-an-email", testPassword); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad email = %v", err)
	}
	id := h.user("api@example.test")
	if _, err := h.svc.CreateUser(ctx, "API@example.test", testPassword); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("duplicate = %v", err)
	}
	c := h.client()
	c.signIn("api@example.test")

	// SetPassword replaces the password and signs the user out everywhere
	newPass := "another long passphrase"
	if err := h.svc.SetPassword(ctx, id, newPass); err != nil {
		t.Fatal(err)
	}
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
	expectProblem(t, h.client().signIn("api@example.test"), 401, "invalid_credentials")
	if r := c.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{"credential": "api@example.test", "password": newPass}); r.status != http.StatusOK {
		t.Fatalf("new password = %d %s", r.status, r.body)
	}
	if err := h.svc.SetPassword(ctx, "missing", newPass); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing user = %v", err)
	}
	if err := h.svc.SetPassword(ctx, id, strings.Repeat("y", 129)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long password = %v", err)
	}

	// RevokeSessions
	if err := h.svc.RevokeSessions(ctx, id); err != nil {
		t.Fatal(err)
	}
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")

	// DeleteUser cascades
	c.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{"credential": "api@example.test", "password": newPass})
	if err := h.svc.DeleteUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, coll := range []string{"auth_users", "auth_sessions"} {
		n, _ := h.db.Collection(coll).CountDocuments(ctx, bson.M{})
		if n != 0 {
			t.Fatalf("%s still has %d rows", coll, n)
		}
	}
	if err := h.svc.DeleteUser(ctx, id); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err := h.svc.HasMFA(ctx, id); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("HasMFA of a deleted user = %v", err)
	}
	if h.svc.Limen() == nil {
		t.Fatal("Limen()")
	}
	_ = limen.ErrRecordNotFound
}

func TestIntegrationModule(t *testing.T) {
	h := newHarness(t, nil)
	srv, err := httpapi.New(httpapi.Options{PublicURL: h.base, AccessLog: quiet(), SecurityLog: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Log: quiet()})
	m := &Module{Options: h.svc.o}
	if err := m.Start(context.Background(), a); err == nil {
		t.Fatal("start without dependencies must fail")
	}
	app.Provide(a, h.db)
	if err := m.Start(context.Background(), a); err == nil {
		t.Fatal("start without bus must fail")
	}
	app.Provide(a, h.bus)
	if err := m.Start(context.Background(), a); err == nil {
		t.Fatal("start without server must fail")
	}
	app.Provide(a, srv)
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if s, ok := app.Get[*Service](a); !ok || s == nil {
		t.Fatal("service not provided")
	}
	if m.Name() != "auth" || len(m.Roles()) != 1 || m.Stop(context.Background()) != nil {
		t.Fatal("module metadata")
	}
	bad := &Module{Options: Options{PublicURL: h.base, Auth: h.svc.o.Auth}}
	bad.Options.Auth.Secret = "short"
	a2 := app.New(app.Options{Log: quiet()})
	app.Provide(a2, h.db)
	app.Provide(a2, h.bus)
	srv2, _ := httpapi.New(httpapi.Options{PublicURL: h.base, AccessLog: quiet(), SecurityLog: quiet()})
	app.Provide(a2, srv2)
	if err := bad.Start(context.Background(), a2); err == nil {
		t.Fatal("bad secret must fail")
	}
}

// Every sign-in that issues a session is reported to OnLogin (users.lastLoginAt); a session rotation
// such as finishing TOTP enrolment is not a sign-in.
func TestIntegrationOnLoginRecordsSignIns(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.MFARequired = false })
	var mu sync.Mutex
	var got []string
	h.svc.OnLogin = func(_ context.Context, authUserID string) error {
		mu.Lock()
		got = append(got, authUserID)
		mu.Unlock()
		return nil
	}
	id := h.user("login@example.test")
	c := h.client()
	c.signIn("login@example.test")
	_, _ = c.enrolTOTP()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != id {
		t.Fatalf("OnLogin calls %v, want [%s]", got, id)
	}
}
