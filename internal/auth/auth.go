// Package auth is EmailBridge's admin authentication (S10 §2): Limen with the credential-password,
// two-factor, passkey and api-key plugins on MongoDB (pkg/limenmongo) and the eb-cache bucket, mounted at
// /v1/auth, plus what EmailBridge adds around it:
//
//   - RFC 9457 problems instead of Limen's {"message": …} error bodies;
//   - the session middleware (sliding cookie refresh, Identity in the request context) and the
//     MFA-required gate;
//   - lockout of /signin/credential per email and per IP, lockout of /two-factor/verify per challenge
//     user, single-use TOTP codes, a common-password check, and hardening of the public OTP-send endpoint;
//   - the authentication method of every session (password, password+totp, passkey, …), recorded on the
//     session row so the MFA gate knows whether the session itself was strongly authenticated;
//   - a programmatic user API for bootstrap and invitations, and system mail through a queue (Mailer).
package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thecodearcher/limen"
	apikey "github.com/thecodearcher/limen/plugins/api-key"
	credentialpassword "github.com/thecodearcher/limen/plugins/credential-password"
	"github.com/thecodearcher/limen/plugins/passkey"
	twofactor "github.com/thecodearcher/limen/plugins/two-factor"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/pkg/limenmongo"
)

// Fixed parts of the Limen setup (S10 §2.1).
const (
	BasePath           = "/v1/auth"
	SessionCookie      = "eb_session"
	ChallengeCookie    = "session_2fa"
	APIKeyPrefix       = "ebk_"
	PasswordMinLength  = 12
	PasswordMaxLength  = 128
	sessionUpdateAge   = 15 * time.Minute
	shortSessionMax    = time.Hour
	defaultRPName      = "EmailBridge"
	challengeTokenType = "2fa_challenge" // two-factor plugin's challenge payload type
	cacheKeyPrefix     = "limen"
)

// Options configure the auth service. They come from config (S9 §2.2, §2.5, §2.12); the package never
// reads the environment.
type Options struct {
	Auth        config.AuthConfig
	PublicURL   string
	CORSOrigins []string
	// Env is EB_ENV; a non-Secure cookie is honoured only in development.
	Env string
	// LoginRate is EB_RATE_LOGIN, applied per client IP to the sign-in, 2FA and passkey ceremonies
	// (zero: 10/min).
	LoginRate config.Rate
	// EmailOTP enables the email-code second factor (EB_SYSMAIL_MODE ≠ off).
	EmailOTP bool
	// RPName is the WebAuthn relying-party name shown by authenticators (default "EmailBridge").
	RPName string
	// Mailer receives OTP and password-reset mail (default: NoopMailer).
	Mailer Mailer
	// Log is the auth-category logger; SecurityLog the security-category one.
	Log         *slog.Logger
	SecurityLog *slog.Logger
}

// Service is the configured Limen instance with EmailBridge's middleware and user API.
type Service struct {
	o        Options
	limen    *limen.Limen
	db       *limenmongo.Adapter
	mongo    *mongo.Database
	kv       KV
	secret   []byte
	mailer   Mailer
	log      *slog.Logger
	security *slog.Logger
	cookie   cookieAttrs
	password credentialpassword.API
	passkeys passkey.API
	handler  http.Handler
	// Cipher encrypts OIDC client secrets (optional until OIDC is used).
	Cipher *crypto.Cipher
	// Audit records OIDC login/fail (optional).
	Audit *audit.Writer
	// Egress dials OIDC discovery / token / JWKS (W35). Nil → secure default.
	Egress *egress.Dialer
	// HTTP overrides the egress-built client when set (tests).
	HTTP *http.Client
	// InviteChecker / ProvisionHook are wired by tenancy for invitation-based SSO and profile create.
	InviteChecker InviteChecker
	ProvisionHook ProvisionHook
	// hasMFA replaces HasMFA in MFAGate (unit tests).
	hasMFA func(ctx context.Context, authUserID string) (bool, error)
	// OnLogin, when set, is told about every sign-in that issued a session (users.lastLoginAt).
	OnLogin func(ctx context.Context, authUserID string) error
	// Failures, when set, records auth failures for eb_auth_failures_total (S12 §9).
	Failures AuthFailureRecorder

	rlFailLog sync.Map // helper name → last warn time (once per window)
}

// AuthFailureRecorder records failed sign-ins by provider label (password, passkey, totp, …).
type AuthFailureRecorder interface {
	AuthFailure(provider string)
}

type cookieAttrs struct {
	secure bool
	domain string
}

// decodeSecret accepts EB_AUTH_SECRET as a raw 32-byte string or standard base64 of 32 bytes (S9 §2.5).
func decodeSecret(s string) ([]byte, error) {
	if len(s) == 32 {
		return []byte(s), nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("auth: EB_AUTH_SECRET must be 32 bytes (raw or base64)")
	}
	return raw, nil
}

func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	return u.Scheme + "://" + u.Host
}

// trustedOrigins is EB_PUBLIC_URL plus EB_HTTP_CORS_ORIGINS, exact (S10 §2.1).
func (o Options) trustedOrigins() []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range append([]string{o.PublicURL}, o.CORSOrigins...) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if or := originOf(raw); !seen[or] {
			seen[or] = true
			out = append(out, or)
		}
	}
	return out
}

func (o Options) cookieAttrs() cookieAttrs {
	c := cookieAttrs{secure: o.Auth.CookieSecure || o.Env != "development"}
	// A cookie for the public host itself stays host-only (stricter than Domain=host, which would also
	// send it to every subdomain); only a different, parent domain is set explicitly.
	host := ""
	if u, err := url.Parse(o.PublicURL); err == nil {
		host = u.Hostname()
	}
	if d := strings.TrimPrefix(strings.TrimSpace(o.Auth.CookieDomain), "."); d != "" && !strings.EqualFold(d, host) {
		c.domain = d
	}
	return c
}

// sessionTimes derives Limen's session settings, keeping its constraints (UpdateAge < IdleTimeout ≤
// Duration, ShortDuration < Duration) even for unusual configurations.
func (o Options) sessionTimes() (duration, idle, updateAge, short time.Duration) {
	duration = o.Auth.SessionTTL
	if duration <= 0 {
		duration = 12 * time.Hour
	}
	idle = o.Auth.SessionIdle
	if idle <= 0 || idle > duration {
		idle = min(2*time.Hour, duration)
	}
	updateAge = sessionUpdateAge
	if updateAge >= idle {
		updateAge = idle / 2
	}
	short = shortSessionMax
	if short >= duration {
		short = duration / 2
	}
	return duration, idle, updateAge, short
}

func (o Options) loginRule() (int, time.Duration) {
	if o.LoginRate.Count > 0 && o.LoginRate.Period > 0 {
		return o.LoginRate.Count, o.LoginRate.Period
	}
	return 10, time.Minute
}

// uuidGenerator gives every Limen row a random UUIDv4 string ID (opaque passkey user handles, S10 §2.1).
type uuidGenerator struct{}

func (uuidGenerator) Generate(context.Context) (any, error) { return uuid.NewString(), nil }
func (uuidGenerator) GetColumnType() limen.ColumnType       { return limen.ColumnTypeUUID }

// disabledRoutes are Limen routes EmailBridge does not offer: public sign-up (users come from bootstrap
// and invitations) and the api-key plugin's HTTP API (keys are managed by internal/apikeys with scopes
// and organisations).
var disabledRoutes = []string{
	"signup",
	"api-key-create", "api-key-get", "api-key-list", "api-key-update", "api-key-revoke", "api-key-rotate",
	// Limen's list-sessions returns every session's raw bearer token; /v1/me/sessions replaces it without tokens.
	"list-sessions",
}

// loginPaths get EB_RATE_LOGIN (relative to the base path).
var loginPaths = []string{
	"/signin/credential", "/two-factor/verify", "/two-factor/otp/send",
	"/passkeys/begin-authentication", "/passkeys/finish-authentication",
}

// callbacks are the Limen mail hooks (wired to the Service; no-ops for schema generation).
type callbacks struct {
	otp   func(ctx context.Context, email, code string)
	reset func(email, token string)
}

// limenConfig builds the Limen configuration (S10 §2.1). It is shared by New and the schema-file test,
// so the embedded schema always describes exactly the plugin set that runs.
func limenConfig(o Options, db limen.DatabaseAdapter, cache limen.CacheAdapter, secret []byte, cb callbacks) *limen.Config {
	duration, idle, updateAge, short := o.sessionTimes()
	origins := o.trustedOrigins()
	ca := o.cookieAttrs()
	maxReq, window := o.loginRule()

	rl := []limen.RateLimiterOption{limen.WithRateLimiterKeyGenerator(rateLimitKey)}
	for _, p := range loginPaths {
		rl = append(rl, limen.WithRateLimiterCustomRule(p, maxReq, window))
	}
	httpOpts := []limen.HTTPConfigOption{
		limen.WithHTTPBasePath(BasePath),
		limen.WithHTTPSessionCookieName(SessionCookie),
		limen.WithHTTPCookiePath("/"),
		limen.WithHTTPCookieHTTPOnly(true),
		limen.WithHTTPCookieSecure(ca.secure),
		limen.WithHTTPCookieSameSite(http.SameSiteStrictMode),
		limen.WithHTTPTrustedOrigins(origins),
		limen.WithHTTPCSRFProtection(true),
		limen.WithHTTPOriginCheck(true),
		limen.WithHTTPDisabledPaths(append([]string(nil), disabledRoutes...)),
		limen.WithHTTPRateLimiter(rl...),
	}
	if ca.domain != "" {
		httpOpts = append(httpOpts, limen.WithHTTPCookieCrossSubdomainEnabled(ca.domain))
	}

	rpName := o.RPName
	if rpName == "" {
		rpName = defaultRPName
	}
	passkeyOpts := []passkey.ConfigOption{
		passkey.WithRPName(rpName),
		passkey.WithAllowedOrigins(origins...),
		passkey.WithAuthenticatorSelection(passkey.AuthenticatorSelection{
			UserVerification: passkey.UserVerificationRequired,
			ResidentKey:      passkey.ResidentKeyRequired,
		}),
		passkey.WithAllowInternalUserIDAsHandle(true),
	}
	if o.Auth.WebAuthnRPID != "" {
		passkeyOpts = append(passkeyOpts, passkey.WithRPID(o.Auth.WebAuthnRPID))
	}

	return &limen.Config{
		BaseURL:        o.PublicURL,
		Database:       db,
		CacheStore:     cache,
		CacheKeyPrefix: cacheKeyPrefix,
		Secret:         secret,
		Schema:         limen.NewDefaultSchemaConfig(limen.WithSchemaIDGenerator(uuidGenerator{})),
		Session: limen.NewDefaultSessionConfig(
			limen.WithSessionDuration(duration),
			limen.WithSessionIdleTimeout(idle),
			limen.WithSessionUpdateAge(updateAge),
			limen.WithSessionShortDuration(short),
			limen.WithSessionIPAddressExtractor(clientIPString),
		),
		HTTP: limen.NewDefaultHTTPConfig(httpOpts...),
		// Addresses are verified when the user is created (bootstrap by the operator, invitations by the
		// one-time link), so Limen's self-service verification flow and its public endpoints stay off.
		Email: limen.NewDefaultEmailConfig(limen.WithEmailVerification(limen.WithDisableEmailVerification())),
		Plugins: []limen.Plugin{
			credentialpassword.New(
				credentialpassword.WithPasswordMinLength(PasswordMinLength),
				credentialpassword.WithPasswordRequireUppercase(false),
				credentialpassword.WithPasswordRequireNumbers(false),
				credentialpassword.WithPasswordRequireSymbols(false),
				credentialpassword.WithSendPasswordResetEmail(cb.reset),
			),
			twofactor.New(
				// explicit, so LIMEN_SECRET / LIMEN_TOTP_SECRET in the environment can never override it
				twofactor.WithSecret(string(secret)),
				twofactor.WithTOTP(twofactor.WithTOTPIssuer(rpName)),
				twofactor.WithOTP(
					twofactor.WithOTPEnabled(o.EmailOTP),
					twofactor.WithOTPSendCode(func(ctx context.Context, u *twofactor.UserWithTwoFactor, code string) {
						if u != nil && u.User != nil {
							cb.otp(ctx, u.Email, code)
						}
					}),
				),
				twofactor.WithBackupCodes(),
			),
			passkey.New(passkeyOpts...),
			apikey.New(apikey.WithProfiles(apikey.Profile{ID: "default", Prefix: APIKeyPrefix})),
		},
	}
}

// rateLimitKey keys Limen's limiter on the trusted-proxy client IP (never X-Forwarded-For from anyone,
// SP3 finding 3).
func rateLimitKey(r *http.Request) string { return "ip:" + clientIPString(r) }

// clientIPString is the client IP established by httpapi's trusted-proxy rules, falling back to the
// peer address when the request did not pass through httpapi.
func clientIPString(r *http.Request) string {
	if ip := httpapi.ClientIP(r.Context()); ip.IsValid() {
		return ip.String()
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// New builds Limen on db (the EmailBridge database; Limen collections are auth_*) and kv, creates the
// adapter's indexes and the wrapped HTTP handler.
func New(ctx context.Context, o Options, db *mongo.Database, kv KV) (*Service, error) {
	if db == nil || kv == nil {
		return nil, errors.New("auth: database and cache are required")
	}
	if o.PublicURL == "" {
		return nil, errors.New("auth: EB_PUBLIC_URL is required")
	}
	secret, err := decodeSecret(o.Auth.Secret)
	if err != nil {
		return nil, err
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.SecurityLog == nil {
		o.SecurityLog = o.Log
	}
	adapter, err := limenmongo.New(db)
	if err != nil {
		return nil, err
	}
	if err := adapter.EnsureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	s := &Service{
		o:        o,
		db:       adapter,
		mongo:    db,
		kv:       kv,
		secret:   secret,
		mailer:   o.Mailer,
		log:      o.Log,
		security: o.SecurityLog,
		cookie:   o.cookieAttrs(),
	}
	if s.mailer == nil {
		s.mailer = NoopMailer{Log: o.Log}
	}
	l, err := limen.New(limenConfig(o, adapter, &limenCache{kv: kv, log: o.Log}, secret, callbacks{otp: s.sendOTP, reset: s.sendPasswordReset}))
	if err != nil {
		return nil, fmt.Errorf("auth: limen: %w", err)
	}
	s.limen = l
	pw, ok := limen.TryUse[credentialpassword.API](l, limen.PluginCredentialPassword)
	if !ok {
		return nil, errors.New("auth: credential-password plugin missing")
	}
	pk, ok := limen.TryUse[passkey.API](l, limen.PluginPasskey)
	if !ok {
		return nil, errors.New("auth: passkey plugin missing")
	}
	s.password, s.passkeys = pw, pk
	s.handler = s.wrap(l.Handler())
	return s, nil
}

// Limen returns the underlying Limen instance (internal/apikeys uses its api-key plugin).
func (s *Service) Limen() *limen.Limen { return s.limen }

// Handler is Limen's handler with EmailBridge's guards and problem translation. Mount it at
// /v1/auth/* behind Middleware and MFAGate (Mount does all three).
func (s *Service) Handler() http.Handler { return s.handler }

// Mount wires the service into the HTTP server: the session middleware and the MFA gate in front of every
// route, Limen at /v1/auth/*, OIDC under /v1/auth/oidc/*, admin OIDC CRUD, and the own-sessions API.
func (s *Service) Mount(srv *httpapi.Server) {
	srv.AddMiddleware(s.Middleware)
	srv.AddMiddleware(s.MFAGate)
	srv.Router.Handle(BasePath+"/oidc/*", http.HandlerFunc(s.handleOIDC))
	srv.Router.Handle(BasePath+"/*", s.Handler())
	s.registerSessions(srv)
	s.registerOIDCAdmin(srv)
	s.documentRoutes(srv)
}

// wrap composes the guards around Limen: outermost first.
func (s *Service) wrap(h http.Handler) http.Handler {
	h = s.translate(h)
	h = s.recordMethod(h)
	h = s.passwordPolicy(h)
	h = s.totpReplay(h)
	h = s.mfaLockout(h)
	h = s.otpGuard(h)
	h = s.lockout(h)
	return h
}
