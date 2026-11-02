package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

// Authentication methods recorded on a session (Identity.Method).
const (
	MethodPassword        = "password"
	MethodPasswordTOTP    = "password+totp"
	MethodPasswordOTP     = "password+otp"
	MethodPasswordBackup  = "password+backup_code"
	MethodPasswordPasskey = "password+passkey" // password session upgraded by registering a passkey
	MethodPasskey         = "passkey"
	MethodUnknown         = "unknown"
)

// methodField is the session-row field (auth_sessions) that stores the method.
const methodField = "eb_auth_method"

// mfaMethod reports whether a session authenticated with method satisfies MFA: a user-verified passkey on
// its own, or a password plus a second factor (S10 §2.2).
func mfaMethod(method string) bool {
	switch method {
	case MethodPasswordTOTP, MethodPasswordOTP, MethodPasswordBackup, MethodPasswordPasskey, MethodPasskey, MethodOIDC:
		return true
	}
	return false
}

// Identity is the signed-in admin user of a request (session cookie), set by Middleware.
type Identity struct {
	// AuthUserID is Limen's user ID (UUIDv4); EmailBridge's users.authUserId links the profile.
	AuthUserID string
	Email      string
	// SessionID is the session row's ID (not the token).
	SessionID string
	// MFA reports whether this session was strongly authenticated (see Method).
	MFA bool
	// Method is how the session was established: password, password+totp, password+otp,
	// password+backup_code, password+passkey, passkey, oidc or unknown.
	Method string
}

type identityKey struct{}

type sessionStateKey struct{}

// sessionState records why a request with a session cookie has no identity.
type sessionState struct{ expired bool }

// IdentityFrom returns the identity Middleware stored in ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// WithIdentity returns ctx carrying id (tests and internal callers).
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// Require returns the identity of ctx, or an unauthenticated / session_expired error for handlers.
func Require(ctx context.Context) (Identity, error) {
	if id, ok := IdentityFrom(ctx); ok {
		return id, nil
	}
	if st, ok := ctx.Value(sessionStateKey{}).(sessionState); ok && st.expired {
		return Identity{}, errs.New(errs.CodeSessionExpired, "The session has expired; sign in again.")
	}
	return Identity{}, errs.New(errs.CodeUnauthenticated, "Sign in to continue.")
}

// RequireSession refuses requests without a signed-in identity: 401 session_expired when the cookie's
// session expired, 401 unauthenticated otherwise.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Require(r.Context()); err != nil {
			writeErr(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var e *errs.E
	if errors.As(err, &e) {
		problem(w, r, e.Code, e.Msg)
		return
	}
	problem(w, r, errs.CodeInternalError, "")
}

// Middleware resolves the session cookie on /v1 requests (S10 §2.3): it calls Limen's GetSession,
// re-sets the cookie when Limen extended the session (sliding expiry), and stores the Identity in the
// context with the actor for logging. A request without a valid session simply has no identity; handlers
// decide (RequireSession).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(SessionCookie); err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		vs, err := s.limen.GetSession(r)
		switch {
		case err == nil && vs != nil && vs.User != nil && vs.Session != nil:
		case errors.Is(err, limen.ErrSessionExpired):
			s.clearCookie(w, r)
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, sessionStateKey{}, sessionState{expired: true})))
			return
		case err == nil, errors.Is(err, limen.ErrSessionNotFound), errors.Is(err, limen.ErrRecordNotFound):
			s.clearCookie(w, r)
			next.ServeHTTP(w, r)
			return
		default:
			s.log.ErrorContext(ctx, "session lookup failed", "err", err)
			problem(w, r, errs.CodeServiceUnavailable, "Sessions cannot be checked right now; try again.")
			return
		}
		if vs.Refreshed != nil && vs.Refreshed.Cookie != nil {
			http.SetCookie(w, vs.Refreshed.Cookie)
		}
		method, _ := vs.Session.Raw()[methodField].(string)
		if method == "" {
			method = MethodUnknown
		}
		id := Identity{
			AuthUserID: fmt.Sprint(vs.User.ID),
			Email:      vs.User.Email,
			SessionID:  fmt.Sprint(vs.Session.ID),
			MFA:        mfaMethod(method),
			Method:     method,
		}
		ctx = WithIdentity(ctx, id)
		// P0.14 replaces the actor with the usr_… profile ID once the profile is resolved.
		ctx = logging.With(ctx, func(i *logging.IDs) { i.Actor = "auth:" + id.AuthUserID })
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clearCookie deletes a stale session cookie (outside Limen's own routes, which manage it themselves).
func (s *Service) clearCookie(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, BasePath+"/") {
		return
	}
	//nolint:gosec // G124: Secure follows EB_AUTH_COOKIE_SECURE, which may be off only with EB_ENV=development
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, Domain: s.cookie.domain,
		HttpOnly: true, Secure: s.cookie.secure, SameSite: http.SameSiteStrictMode,
	})
}

// alwaysAllowed is what a signed-in user whose session did not pass MFA may always do while
// EB_AUTH_MFA_REQUIRED is on (S10 §2.2): see who they are, sign out, and use Limen's public sign-in and
// reset flows.
var alwaysAllowed = map[string]bool{
	"GET /v1/me":                                           true,
	"GET " + BasePath + "/me":                              true,
	"POST " + BasePath + "/signout":                        true,
	"POST " + BasePath + "/signin/credential":              true,
	"POST " + BasePath + "/two-factor/verify":              true,
	"POST " + BasePath + "/two-factor/otp/send":            true,
	"GET " + BasePath + "/passkeys/begin-authentication":   true,
	"POST " + BasePath + "/passkeys/finish-authentication": true,
	"POST " + BasePath + "/passwords/request-reset":        true,
	"POST " + BasePath + "/passwords/reset":                true,
}

// enrolment is the first-factor enrolment (TOTP or a passkey). It is open to a non-MFA session only while
// the user has no second factor at all: otherwise a stolen password would let an attacker enrol their own
// authenticator and lift the session to MFA.
var enrolment = map[string]bool{
	"POST " + BasePath + "/two-factor/initiate-setup":    true,
	"POST " + BasePath + "/two-factor/finalize-setup":    true,
	"GET " + BasePath + "/two-factor/totp/uri":           true,
	"GET " + BasePath + "/passkeys/begin-registration":   true,
	"POST " + BasePath + "/passkeys/finish-registration": true,
}

// MFAGate enforces EB_AUTH_MFA_REQUIRED: a signed-in identity whose session did not pass MFA may only use
// the always-allowed endpoints (including own-session list/revoke), plus first enrolment when the user
// has no factor yet; everything else answers 401 mfa_required. Requests without an identity pass.
func (s *Service) MFAGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		path := strings.TrimRight(r.URL.Path, "/")
		key := r.Method + " " + path
		if !s.o.Auth.MFARequired || !ok || id.MFA || alwaysAllowed[key] || sessionsPathAllowed(r.Method, path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if enrolment[key] {
			has, err := s.mfaCheck(ctx, id.AuthUserID)
			if err != nil {
				s.log.ErrorContext(ctx, "mfa enrolment check failed", "err", err)
				problem(w, r, errs.CodeServiceUnavailable, "")
				return
			}
			if !has {
				next.ServeHTTP(w, r)
				return
			}
			s.security.WarnContext(ctx, "enrolment from a password-only session refused", "path", r.URL.Path, "ip", clientIPString(r))
			problem(w, r, errs.CodeMFARequired, "This account already has a second factor; sign in with your passkey or code to add another.")
			return
		}
		s.log.InfoContext(ctx, "request refused until MFA enrolment", "path", r.URL.Path, "method", id.Method)
		problem(w, r, errs.CodeMFARequired, "Set up two-factor authentication or a passkey, or sign in with one, to continue.")
	})
}

func (s *Service) mfaCheck(ctx context.Context, authUserID string) (bool, error) {
	if s.hasMFA != nil {
		return s.hasMFA(ctx, authUserID)
	}
	return s.HasMFA(ctx, authUserID)
}
