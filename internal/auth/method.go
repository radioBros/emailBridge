package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/thecodearcher/limen"
)

// recordMethod stores on the session row how each new session was authenticated (S10 §2.2), because
// Limen's sessions do not say. It hooks the response just before the status is written: if Limen issued a
// session cookie, the request path tells the method; any other route that rotates the session (password
// change, 2FA disable) carries the method of the session it replaced. A passkey registered from a
// password-only session upgrades that session to password+passkey (the registration required user
// verification), mirroring Limen's rotation after TOTP enrolment.
func (s *Service) recordMethod(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if route(r) == "/two-factor/verify" {
			body = peekJSON(r)
		}
		mw := &methodWriter{ResponseWriter: w, onHeader: func(status int, h http.Header) {
			if status < 200 || status > 299 {
				return
			}
			ctx := r.Context()
			current, hasSession := IdentityFrom(ctx)
			if token := issuedToken(h); token != "" {
				method := methodFor(route(r), body, current, hasSession)
				s.setMethod(ctx, token, method)
				s.log.InfoContext(ctx, "session issued", "route", route(r), "method", method)
				if signInRoutes[route(r)] {
					s.loggedIn(ctx, token)
				}
				return
			}
			if route(r) == "/passkeys/finish-registration" && hasSession && current.Method == MethodPassword {
				if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
					s.setMethod(ctx, c.Value, MethodPasswordPasskey)
				}
			}
		}}
		next.ServeHTTP(mw, r)
	})
}

// signInRoutes issue a session for a sign-in; other routes that issue one rotate an existing session.
var signInRoutes = map[string]bool{
	"/signin/credential":              true, // only issues a session when no second factor is pending
	"/two-factor/verify":              true,
	"/passkeys/finish-authentication": true,
}

// loggedIn reports the sign-in to OnLogin. Failures are logged: a missed timestamp must not fail a login.
func (s *Service) loggedIn(ctx context.Context, token string) {
	if s.OnLogin == nil {
		return
	}
	row, err := s.db.FindOne(ctx, "sessions", []limen.Where{limen.Eq("token", token)}, nil)
	if err != nil || row == nil {
		s.log.WarnContext(ctx, "cannot record the sign-in time: session not found", "err", err)
		return
	}
	userID := fmt.Sprint(row["user_id"])
	if err := s.OnLogin(ctx, userID); err != nil {
		s.log.WarnContext(ctx, "cannot record the sign-in time", "err", err)
	}
}

func methodFor(path string, verifyBody map[string]any, current Identity, hasSession bool) string {
	switch path {
	case "/signin/credential":
		return MethodPassword
	case "/two-factor/verify":
		code, _, _ := limenString(verifyBody, "code")
		method, _, _ := limenString(verifyBody, "method")
		switch {
		case looksLikeBackupCode(code):
			return MethodPasswordBackup
		case method == "otp":
			return MethodPasswordOTP
		default:
			return MethodPasswordTOTP
		}
	case "/two-factor/finalize-setup":
		return MethodPasswordTOTP
	case "/passkeys/finish-authentication":
		return MethodPasskey
	}
	if hasSession && current.Method != "" {
		return current.Method
	}
	return MethodUnknown
}

func (s *Service) setMethod(ctx context.Context, token, method string) {
	res, err := s.db.Update(ctx, "sessions", []limen.Where{limen.Eq("token", token)}, map[string]any{methodField: method})
	if err != nil || res.RowsAffected == 0 {
		// without a method the session counts as not MFA-authenticated, which is the safe side
		s.log.ErrorContext(ctx, "session method not recorded", "err", err, "rows", res.RowsAffected)
	}
}

// issuedToken returns the token of the last non-empty eb_session cookie set on the response (a refreshed
// old session may precede the new one).
func issuedToken(h http.Header) string {
	token := ""
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == SessionCookie && c.Value != "" && c.MaxAge >= 0 {
			token = c.Value
		}
	}
	return token
}

// methodWriter calls onHeader once, with the final headers, before the status line is written.
type methodWriter struct {
	http.ResponseWriter
	onHeader func(status int, h http.Header)
	done     bool
}

func (w *methodWriter) WriteHeader(code int) {
	if !w.done {
		w.done = true
		w.onHeader(code, w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *methodWriter) Write(b []byte) (int, error) {
	if !w.done {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *methodWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
