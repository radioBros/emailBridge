package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

// OTP send limits (S10 §2.4: at most 5 code mails per 15 minutes per user).
const (
	otpSendMax    = 5
	otpSendWindow = 15 * time.Minute
	// totpUsedTTL covers one TOTP step of skew on each side (30 s) with margin (OWASP ASVS V2.8).
	totpUsedTTL = 3 * time.Minute
)

func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

func lockKeyEmail(email string) string { return "eb:auth:lockout:email:" + hashKey(email) }
func lockKeyIP(ip string) string       { return "eb:auth:lockout:ip:" + ip }
func lockKeyMFA(userID string) string  { return "eb:auth:lockout:mfa:" + userID }
func otpSendKey(userID string) string  { return "eb:auth:otp-send:" + userID }

func totpUsedKey(userID, code string) string {
	sum := sha256.Sum256([]byte(code))
	return "totp-used:" + userID + ":" + hex.EncodeToString(sum[:])
}

// lockout guards POST /v1/auth/signin/credential (S10 §2.4): failures are counted per email and per client
// IP for EB_AUTH_LOCKOUT_DURATION; at EB_AUTH_LOCKOUT_THRESHOLD the email or IP is locked for the full
// duration and the endpoint answers 423 account_locked without calling Limen. Unknown emails are counted
// the same way, so the answer never reveals whether an account exists.
func (s *Service) lockout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		threshold, dur := int64(s.o.Auth.LockoutThreshold), s.o.Auth.LockoutDuration
		if r.Method != http.MethodPost || route(r) != "/signin/credential" || threshold <= 0 || dur <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		email := limen.NormalizeEmail(stringField(peekJSON(r), "credential"))
		ip := clientIPString(r)
		keys := []string{lockKeyIP(ip)}
		if email != "" {
			keys = append(keys, lockKeyEmail(email))
		}
		for _, k := range keys {
			e, err := s.kv.CacheGet(ctx, k)
			if errors.Is(err, bus.ErrCacheMiss) {
				continue
			}
			if err != nil {
				s.log.WarnContext(ctx, "lockout check failed", "err", err)
				continue
			}
			var n int64
			if json.Unmarshal(e.Value, &n) == nil && n >= threshold {
				s.log.WarnContext(ctx, "sign-in refused: locked", logging.Address("email", email), "ip", ip)
				if !e.ExpiresAt.IsZero() {
					retryAfter(w, e.ExpiresAt)
				}
				problem(w, r, errs.CodeAccountLocked, "Too many failed sign-ins; try again later.")
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		switch rec.code() {
		case http.StatusOK:
			if email != "" {
				if err := s.kv.CacheDelete(ctx, lockKeyEmail(email)); err != nil {
					s.log.WarnContext(ctx, "lockout reset failed", "err", err)
				}
			}
		case http.StatusUnauthorized:
			if s.Failures != nil {
				s.Failures.AuthFailure("password")
			}
			var count int64
			for _, k := range keys {
				n, err := s.kv.CacheAdd(ctx, k, 1, dur)
				if err != nil {
					s.log.WarnContext(ctx, "lockout count failed", "err", err)
					continue
				}
				count = max(count, n)
				if n == threshold {
					// the lock lasts the full duration from the failure that triggered it
					if err := s.kv.CacheExpire(ctx, k, dur); err != nil {
						s.log.WarnContext(ctx, "lockout expiry failed", "err", err)
					}
				}
			}
			s.log.InfoContext(ctx, "sign-in failed", logging.Address("email", email), "ip", ip, "failures", count)
			if count >= threshold {
				s.log.WarnContext(ctx, "sign-in locked", logging.Address("email", email), "ip", ip, "duration", dur)
			}
		}
	})
}

// challenge is the payload of the two-factor plugin's encrypted session_2fa cookie.
type challenge struct {
	UserID any    `json:"user_id"`
	Exp    int64  `json:"exp"`
	Type   string `json:"type"`
}

// challengeUser decrypts and checks the session_2fa cookie and returns the user ID it was issued for.
func (s *Service) challengeUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(ChallengeCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	plain, err := limen.DecryptXChaCha(c.Value, s.secret, nil)
	if err != nil {
		return "", false
	}
	var ch challenge
	if json.Unmarshal([]byte(plain), &ch) != nil || ch.Type != challengeTokenType || time.Now().Unix() > ch.Exp {
		return "", false
	}
	id, ok := ch.UserID.(string)
	return id, ok && id != ""
}

// otpGuard hardens POST /v1/auth/two-factor/otp/send, which Limen leaves public (SP3 finding 2): it
// requires a valid session_2fa challenge whose user owns the requested email, and allows at most 5 sends
// per 15 minutes per user.
func (s *Service) otpGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || route(r) != "/two-factor/otp/send" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		email := limen.NormalizeEmail(stringField(peekJSON(r), "email"))
		userID, ok := s.challengeUser(r)
		if !ok {
			s.security.WarnContext(ctx, "otp send without sign-in challenge refused", "ip", clientIPString(r))
			problem(w, r, errs.CodeUnauthenticated, "Sign in with your password first; then request a code.")
			return
		}
		row, err := s.db.FindOne(ctx, "users", []limen.Where{limen.Eq("id", userID)}, nil)
		if err != nil && !errors.Is(err, limen.ErrRecordNotFound) {
			s.log.ErrorContext(ctx, "otp send: user lookup failed", "err", err)
			problem(w, r, errs.CodeServiceUnavailable, "")
			return
		}
		if err != nil || email == "" || limen.NormalizeEmail(stringField(row, "email")) != email {
			s.security.WarnContext(ctx, "otp send for another address refused", logging.Address("email", email), "ip", clientIPString(r))
			problem(w, r, errs.CodeUnauthenticated, "The code can only be sent to the account that is signing in.")
			return
		}
		n, err := s.cacheAddFailClosed(ctx, otpSendKey(userID), otpSendWindow, "otp-send")
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if n > otpSendMax {
			if e, gerr := s.kv.CacheGet(ctx, otpSendKey(userID)); gerr == nil && !e.ExpiresAt.IsZero() {
				retryAfter(w, e.ExpiresAt)
			}
			s.log.WarnContext(ctx, "otp send limit reached", logging.Address("email", email))
			problem(w, r, errs.CodeRateLimited, "Too many codes requested; wait before asking for another.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// passwordFields are the password-setting routes and their new-password field.
var passwordFields = map[string]string{
	"POST /passwords/reset":  "new_password",
	"POST /passwords/change": "new_password",
	"PUT /passwords":         "new_password",
}

// passwordPolicy enforces the maximum password length (128) and the common-password check on Limen's
// password-setting routes; Limen checks only the minimum. Values are TrimSpace'd like Limen's validator.
func (s *Service) passwordPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		field, ok := passwordFields[r.Method+" "+route(r)]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		pw, _, bad := limenString(peekJSON(r), field)
		if bad {
			problem(w, r, errs.CodeValidationFailed, "Invalid request.")
			return
		}
		if len([]rune(pw)) > PasswordMaxLength {
			problem(w, r, errs.CodeValidationFailed, "The password must be at most 128 characters.")
			return
		}
		if isCommonPassword(pw) {
			problem(w, r, errs.CodeValidationFailed, "Choose a less common password.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// looksLikeBackupCode mirrors limen plugins/two-factor.looksLikeBackupCode (v0.2.1).
func looksLikeBackupCode(code string) bool {
	return strings.Contains(code, "-")
}

// totpVerifyCode returns the normalised TOTP code to claim for r, or ok=false when the request is not a
// TOTP possession check (backup / email OTP / other route). bad is true when code or method is present
// but not a string — Limen rejects those; we must not skip the guard as "not TOTP".
func totpVerifyCode(r *http.Request, body map[string]any) (code string, ok, bad bool) {
	if r.Method != http.MethodPost {
		return "", false, false
	}
	path := route(r)
	if path != "/two-factor/verify" && path != "/two-factor/finalize-setup" {
		return "", false, false
	}
	code, codeOK, codeBad := limenString(body, "code")
	method, _, methodBad := limenString(body, "method")
	if codeBad || (path == "/two-factor/verify" && methodBad) {
		return "", false, true
	}
	if !codeOK || code == "" || looksLikeBackupCode(code) {
		return "", false, false
	}
	if path == "/two-factor/verify" {
		// Limen defaults method to totp when absent; otp and anything else are not TOTP claims.
		if method == "otp" || (method != "" && method != "totp") {
			return "", false, false
		}
	}
	return code, true, false
}

// totpUserID is the account a TOTP code is being checked for: the session_2fa challenge on verify, or
// the signed-in session on authenticated enrolment routes.
func (s *Service) totpUserID(r *http.Request) (string, bool) {
	switch route(r) {
	case "/two-factor/verify":
		return s.challengeUser(r)
	case "/two-factor/finalize-setup":
		id, ok := IdentityFrom(r.Context())
		return id.AuthUserID, ok && id.AuthUserID != ""
	}
	return "", false
}

// totpReplay makes TOTP codes single-use within their validity window (OWASP ASVS V2.8): it atomically
// claims totp-used:<userID>:<sha256(code)> (TTL 3 min) before calling Limen; a second attempt with the
// same code gets 401 mfa_invalid. If Limen then rejects the code the claim is released so a mistyped
// attempt does not burn a still-valid code. Cache failure fails closed (503). Code/method are
// TrimSpace'd like Limen so "123456 " cannot bypass the claim key.
func (s *Service) totpReplay(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := peekJSON(r)
		code, apply, bad := totpVerifyCode(r, body)
		if bad {
			problem(w, r, errs.CodeValidationFailed, "Invalid request.")
			return
		}
		if !apply {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		userID, ok := s.totpUserID(r)
		if !ok {
			problem(w, r, errs.CodeUnauthenticated, "Sign in first to continue.")
			return
		}
		key := totpUsedKey(userID, code)
		n, err := s.kv.CacheAdd(ctx, key, 1, totpUsedTTL)
		if err != nil {
			s.log.WarnContext(ctx, "totp replay claim failed", "err", err)
			problem(w, r, errs.CodeServiceUnavailable, "Authentication is temporarily unavailable; try again shortly.")
			return
		}
		if n != 1 {
			s.log.InfoContext(ctx, "totp code reuse refused", "user_id", userID)
			problem(w, r, errs.CodeMFAInvalid, "That code has already been used.")
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.code() == http.StatusUnauthorized {
			if err := s.kv.CacheDelete(ctx, key); err != nil {
				s.log.WarnContext(ctx, "totp replay release failed", "err", err)
			}
		}
	})
}

// mfaLockout guards POST /v1/auth/two-factor/verify with the same threshold and duration as sign-in
// lockout (EB_AUTH_LOCKOUT_*), counted per challenge user. Locked challenges get 423 account_locked
// without calling Limen; every failure and lock is logged in auth, and the lock also in security.
func (s *Service) mfaLockout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		threshold, dur := int64(s.o.Auth.LockoutThreshold), s.o.Auth.LockoutDuration
		if r.Method != http.MethodPost || route(r) != "/two-factor/verify" || threshold <= 0 || dur <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		userID, ok := s.challengeUser(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		key := lockKeyMFA(userID)
		e, err := s.kv.CacheGet(ctx, key)
		if err != nil && !errors.Is(err, bus.ErrCacheMiss) {
			s.log.WarnContext(ctx, "2fa lockout check failed", "err", err)
		} else if err == nil {
			var n int64
			if json.Unmarshal(e.Value, &n) == nil && n >= threshold {
				s.log.WarnContext(ctx, "2fa verify refused: locked", "user_id", userID)
				if !e.ExpiresAt.IsZero() {
					retryAfter(w, e.ExpiresAt)
				}
				problem(w, r, errs.CodeAccountLocked, "Too many failed verification attempts; try again later.")
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		switch rec.code() {
		case http.StatusOK:
			if err := s.kv.CacheDelete(ctx, key); err != nil {
				s.log.WarnContext(ctx, "2fa lockout reset failed", "err", err)
			}
		case http.StatusUnauthorized:
			if s.Failures != nil {
				s.Failures.AuthFailure("mfa")
			}
			n, err := s.kv.CacheAdd(ctx, key, 1, dur)
			if err != nil {
				s.log.WarnContext(ctx, "2fa lockout count failed", "err", err)
				return
			}
			s.log.InfoContext(ctx, "2fa verify failed", "user_id", userID, "failures", n)
			if n == threshold {
				if err := s.kv.CacheExpire(ctx, key, dur); err != nil {
					s.log.WarnContext(ctx, "2fa lockout expiry failed", "err", err)
				}
			}
			if n >= threshold {
				s.log.WarnContext(ctx, "2fa verify locked", "user_id", userID, "duration", dur)
				s.security.WarnContext(ctx, "2fa verify locked", "user_id", userID, "duration", dur)
			}
		}
	})
}
