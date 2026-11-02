package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (s *Service) oidcCallbackURL(providerID string) string {
	return strings.TrimRight(s.o.PublicURL, "/") + BasePath + "/oidc/" + providerID + "/callback"
}

func (s *Service) trustedRedirect(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return strings.TrimRight(s.o.PublicURL, "/") + "/", true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme == "" && u.Host == "" {
		// relative path
		if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
			return "", false
		}
		return strings.TrimRight(s.o.PublicURL, "/") + u.Path, true
	}
	origin := u.Scheme + "://" + u.Host
	for _, t := range s.o.trustedOrigins() {
		if strings.EqualFold(t, origin) {
			return raw, true
		}
	}
	return "", false
}

func (s *Service) oidcRateLimit(ctx context.Context, r *http.Request) error {
	maxReq, window := s.o.loginRule()
	n, err := s.cacheAddFailClosed(ctx, "eb:oidc:rl:"+clientIPString(r), window, "oidc")
	if err != nil {
		return err
	}
	if n > int64(maxReq) {
		return errs.New(errs.CodeRateLimited, "Too many sign-in attempts; try again later.")
	}
	return nil
}

func (s *Service) startOIDC(ctx context.Context, w http.ResponseWriter, r *http.Request, providerID string) {
	if err := s.oidcRateLimit(ctx, r); err != nil {
		writeErr(w, r, err)
		return
	}
	p, err := s.getOIDCProvider(ctx, providerID)
	if err != nil || !p.Enabled {
		writeErr(w, r, errs.New(errs.CodeNotFound, "No such OIDC provider."))
		return
	}
	redirect, ok := s.trustedRedirect(r.URL.Query().Get("redirect_uri"))
	if !ok {
		writeErr(w, r, errs.New(errs.CodeValidationFailed, "redirect_uri is not trusted."))
		return
	}
	state, err := randomURLString(24)
	if err != nil {
		problem(w, r, errs.CodeInternalError, "")
		return
	}
	nonce, err := randomURLString(24)
	if err != nil {
		problem(w, r, errs.CodeInternalError, "")
		return
	}
	verifier, err := randomURLString(48)
	if err != nil {
		problem(w, r, errs.CodeInternalError, "")
		return
	}
	if err := s.putOIDCState(ctx, state, oidcState{
		ProviderID: providerID, Verifier: verifier, Nonce: nonce, RedirectURI: redirect,
	}); err != nil {
		s.log.ErrorContext(ctx, "oidc state store failed", "err", err)
		problem(w, r, errs.CodeServiceUnavailable, "")
		return
	}
	provider, err := s.oidcProvider(ctx, p.IssuerURL)
	if err != nil {
		s.security.WarnContext(ctx, "oidc discovery failed", "err", err, "issuer", p.IssuerURL)
		s.failOIDC(ctx, w, r, redirect, "discovery")
		return
	}
	cfg := &oauth2.Config{
		ClientID:    p.ClientID,
		RedirectURL: s.oidcCallbackURL(providerID),
		Scopes:      p.Scopes,
		Endpoint:    provider.Endpoint(),
	}
	authURL := cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oidc.Nonce(nonce),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Service) callbackOIDC(ctx context.Context, w http.ResponseWriter, r *http.Request, providerID string) {
	if err := s.oidcRateLimit(ctx, r); err != nil {
		writeErr(w, r, err)
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	code := q.Get("code")
	st, err := s.takeOIDCState(ctx, state)
	redirect := strings.TrimRight(s.o.PublicURL, "/") + "/login"
	if err == nil && st != nil && st.RedirectURI != "" {
		redirect = st.RedirectURI
	}
	if q.Get("error") != "" || code == "" || state == "" || err != nil || st == nil || st.ProviderID != providerID {
		s.failOIDC(ctx, w, r, redirect, "state")
		return
	}
	p, err := s.getOIDCProvider(ctx, providerID)
	if err != nil || !p.Enabled {
		s.failOIDC(ctx, w, r, redirect, "provider")
		return
	}
	secret, err := s.decryptOIDCSecret(ctx, p)
	if err != nil {
		s.log.ErrorContext(ctx, "oidc secret decrypt failed", "err", err)
		s.failOIDC(ctx, w, r, redirect, "secret")
		return
	}
	tok, err := s.exchangeOIDCCode(ctx, p, secret, code, st.Verifier, s.oidcCallbackURL(providerID))
	if err != nil {
		s.security.WarnContext(ctx, "oidc token exchange failed", "err", err)
		s.failOIDC(ctx, w, r, redirect, "exchange")
		return
	}
	rawID, err := idTokenFromOauthToken(tok)
	if err != nil {
		s.failOIDC(ctx, w, r, redirect, "id_token")
		return
	}
	claims, err := s.verifyIDToken(ctx, p.IssuerURL, p.ClientID, rawID, st.Nonce)
	if err != nil {
		s.security.WarnContext(ctx, "oidc id_token rejected", "err", err)
		s.failOIDC(ctx, w, r, redirect, "verify")
		return
	}
	if claims.Email == "" || !claims.EmailVerified {
		s.failOIDC(ctx, w, r, redirect, "email_verified")
		return
	}
	if !domainAllowed(claims.Email, p.AllowedEmailDomains) {
		s.failOIDC(ctx, w, r, redirect, "domain")
		return
	}
	groups := groupsFromClaims(claims, p.GroupClaim)
	userID, err := s.resolveOIDCUser(ctx, p, claims.Email, groups)
	if err != nil {
		s.failOIDC(ctx, w, r, redirect, "user")
		return
	}
	if err := s.issueOIDCSession(ctx, w, r, userID); err != nil {
		s.log.ErrorContext(ctx, "oidc session issue failed", "err", err)
		s.failOIDC(ctx, w, r, redirect, "session")
		return
	}
	s.auditOIDC(ctx, r, "auth.oidc.login", userID, claims.Email)
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (s *Service) failOIDC(ctx context.Context, w http.ResponseWriter, r *http.Request, redirect, reason string) {
	if s.Failures != nil {
		s.Failures.AuthFailure("oidc")
	}
	s.security.WarnContext(ctx, "oidc sign-in failed", "reason", reason, "ip", clientIPString(r))
	s.auditOIDC(ctx, r, "auth.oidc.fail", "", "")
	if wantsJSON(r) {
		problem(w, r, errs.CodeOIDCFailed, "Sign-in with this provider failed.")
		return
	}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	// Prefer login page with error flag when redirect is the app root.
	var target string
	if strings.TrimRight(redirect, "/") == strings.TrimRight(s.o.PublicURL, "/") ||
		strings.HasSuffix(strings.TrimRight(redirect, "/"), "/") && !strings.Contains(redirect, "/login") {
		target = strings.TrimRight(s.o.PublicURL, "/") + "/login?sso_error=1"
	} else {
		target = redirect + sep + "sso_error=1"
	}
	//nolint:gosec // G710: target is built from trusted PublicURL or a previously validated redirect_uri
	http.Redirect(w, r, target, http.StatusFound)
}

func wantsJSON(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, "application/json") || strings.Contains(a, "application/problem+json")
}

func (s *Service) auditOIDC(ctx context.Context, r *http.Request, action, userID, email string) {
	if s.Audit == nil {
		return
	}
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerSystem})
	ch := audit.Changes{"ip": {After: clientIPString(r)}}
	if email != "" {
		ch["email"] = audit.FieldChange{After: audit.Secret(email)}
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "oidc", ID: userID}, ch)
}

// handleOIDC is mounted under /v1/auth/oidc/* (outside Limen's translator).
func (s *Service) handleOIDC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, BasePath+"/oidc")
	path = strings.Trim(path, "/")
	switch {
	case r.Method == http.MethodGet && path == "providers":
		s.handleListPublicOIDC(ctx, w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/start"):
		id := strings.TrimSuffix(path, "/start")
		id = strings.Trim(id, "/")
		s.startOIDC(ctx, w, r, id)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/callback"):
		id := strings.TrimSuffix(path, "/callback")
		id = strings.Trim(id, "/")
		s.callbackOIDC(ctx, w, r, id)
	default:
		problem(w, r, errs.CodeNotFound, "Not found.")
	}
}

func (s *Service) handleListPublicOIDC(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	list, err := s.listPublicOIDCProviders(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list oidc providers failed", "err", err)
		problem(w, r, errs.CodeInternalError, "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": list})
}
