package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	stateKeyPref = "eb:oauth:state:"
	stateTTL     = 10 * time.Minute
)

type flowState struct {
	AccountID   string `json:"accountId"`
	OrgID       string `json:"orgId"`
	AppID       string `json:"appId"`
	Provider    string `json:"provider"`
	Verifier    string `json:"verifier"`
	RedirectURI string `json:"redirectUri"`
	// LinkID / ReturnURL are set for a hosted connect-link flow (StartForLink): the link is marked used after
	// a successful callback and the browser goes to ReturnURL (validated when the link was created).
	LinkID    string `json:"linkId,omitempty"`
	ReturnURL string `json:"returnUrl,omitempty"`
}

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) putState(ctx context.Context, state string, st flowState) error {
	if s.State == nil {
		return errs.New(errs.CodeServiceUnavailable, "OAuth state store is not available.")
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.State.CacheSet(ctx, stateKeyPref+state, raw, stateTTL)
}

func (s *Service) takeState(ctx context.Context, state string) (*flowState, error) {
	if s.State == nil {
		return nil, errs.New(errs.CodeOauthStateInvalid, "OAuth state is invalid or expired.")
	}
	e, err := s.State.CacheGet(ctx, stateKeyPref+state)
	if err != nil {
		if errors.Is(err, bus.ErrCacheMiss) {
			return nil, errs.New(errs.CodeOauthStateInvalid, "OAuth state is invalid or expired.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.state")
	}
	_ = s.State.CacheDelete(ctx, stateKeyPref+state)
	var st flowState
	if err := json.Unmarshal(e.Value, &st); err != nil {
		return nil, errs.New(errs.CodeOauthStateInvalid, "OAuth state is invalid or expired.")
	}
	return &st, nil
}

// Start begins the authorization-code flow for an account.
func (s *Service) Start(ctx context.Context, accountID string, in StartInput) (*StartResult, error) {
	return s.start(ctx, accountID, in.AppID, "", "")
}

// StartForLink begins the flow for the hosted connect page on behalf of connect link linkID. The link is
// marked used only after the callback stored the tokens (a failed or abandoned consent leaves it usable),
// and the browser is sent to returnURL. returnURL must come from the stored link (validated at link
// creation), never from a request — it is not part of any public API body.
func (s *Service) StartForLink(ctx context.Context, accountID, appID, linkID, returnURL string) (*StartResult, error) {
	if !store.IsID(store.PrefixConnectLink, linkID) {
		return nil, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	return s.start(ctx, accountID, appID, linkID, returnURL)
}

func (s *Service) start(ctx context.Context, accountID, appID, linkID, returnURL string) (*StartResult, error) {
	in := StartInput{AppID: appID}
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if !store.IsID(store.PrefixOAuthApp, in.AppID) {
		return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	a, err := s.Repo.getApp(ctx, in.AppID)
	if err != nil {
		return nil, err
	}
	if !a.Enabled {
		return nil, errs.New(errs.CodeOauthAppDisabled, "The OAuth app is disabled.")
	}
	accProvider, err := s.loadAccountProvider(ctx, org, accountID)
	if err != nil {
		return nil, err
	}
	if !providersCompatible(accProvider, a.Provider) {
		return nil, providerMismatch(accProvider, a.Provider)
	}
	return s.buildAuthorize(ctx, org, accountID, a, linkID, returnURL)
}

func (s *Service) buildAuthorize(ctx context.Context, orgID, accountID string, a *App, linkID, returnURL string) (*StartResult, error) {
	secret, err := s.decryptSecret(ctx, a)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	authURL, tokenURL, _ := s.endpoints(a)
	state, err := randomURLString(24)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.state")
	}
	verifier, err := randomURLString(48)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.pkce")
	}
	redirect := callbackBase(s.CallbackURL) + "/callbacks/" + a.Provider
	if err := s.putState(ctx, state, flowState{
		AccountID: accountID, OrgID: orgID, AppID: a.ID, Provider: a.Provider,
		Verifier: verifier, RedirectURI: redirect, LinkID: linkID, ReturnURL: returnURL,
	}); err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "oauth.state")
	}
	cfg := &oauth2.Config{
		ClientID: a.ClientID, ClientSecret: secret, RedirectURL: redirect,
		Scopes: a.Scopes, Endpoint: oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL},
	}
	u := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", "consent"))
	return &StartResult{AuthorizeURL: u, State: state}, nil
}

func (s *Service) loadAccountProvider(ctx context.Context, orgID, accountID string) (string, error) {
	var doc struct {
		ID       string `bson:"_id"`
		Provider string `bson:"provider"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": orgID,
	}).Decode(&doc)
	if err != nil {
		return "", errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return doc.Provider, nil
}

// HandleCallback processes the provider redirect.
func (s *Service) HandleCallback(ctx context.Context, w http.ResponseWriter, r *http.Request, provider string) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		http.Redirect(w, r, s.oauthErrorURL(""), http.StatusFound)
		return
	}
	state, code := q.Get("state"), q.Get("code")
	if state == "" || code == "" {
		http.Redirect(w, r, s.oauthErrorURL(""), http.StatusFound)
		return
	}
	st, err := s.takeState(ctx, state)
	if err != nil || st.Provider != provider {
		http.Redirect(w, r, s.oauthErrorURL(""), http.StatusFound)
		return
	}
	a, err := s.Repo.GetAppByID(ctx, st.OrgID, st.AppID)
	if err != nil || !a.Enabled {
		http.Redirect(w, r, s.oauthErrorURL(st.AccountID), http.StatusFound)
		return
	}
	secret, err := s.decryptSecret(ctx, a)
	if err != nil {
		http.Redirect(w, r, s.oauthErrorURL(st.AccountID), http.StatusFound)
		return
	}
	authURL, tokenURL, _ := s.endpoints(a)
	cfg := &oauth2.Config{
		ClientID: a.ClientID, ClientSecret: secret, RedirectURL: st.RedirectURI,
		Scopes: a.Scopes, Endpoint: oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL},
	}
	exCtx := context.WithValue(ctx, oauth2.HTTPClient, s.httpClient())
	tok, err := cfg.Exchange(exCtx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		if s.Log != nil {
			s.Log.WarnContext(ctx, "oauth exchange failed", "err", err, "provider", provider)
		}
		http.Redirect(w, r, s.oauthErrorURL(st.AccountID), http.StatusFound)
		return
	}
	if err := s.storeTokens(ctx, st.OrgID, st.AccountID, a, tok); err != nil {
		if s.Log != nil {
			s.Log.ErrorContext(ctx, "oauth store tokens failed", "err", err)
		}
		http.Redirect(w, r, s.oauthErrorURL(st.AccountID), http.StatusFound)
		return
	}
	s.auditAccount(ctx, "account.oauth.connected", st.AccountID, audit.Changes{
		"appId": {After: a.ID}, "provider": {After: a.Provider},
	})
	dest := strings.TrimRight(s.PublicURL, "/") + "/accounts/" + st.AccountID + "?oauth=connected"
	if st.LinkID != "" {
		// Single use: mark the hosted connect link used now that the account is connected. Idempotent — a
		// link already marked (another completed flow) is fine.
		if err := s.Repo.markLinkUsedOnce(ctx, st.OrgID, st.LinkID, s.clock().Truncate(time.Millisecond)); err != nil && s.Log != nil {
			s.Log.WarnContext(ctx, "connect link mark used failed", "err", err, "linkId", st.LinkID)
		}
		if st.ReturnURL != "" {
			dest = st.ReturnURL
		}
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Service) oauthErrorURL(accountID string) string {
	base := strings.TrimRight(s.PublicURL, "/")
	if accountID != "" {
		return base + "/accounts/" + accountID + "?oauth_error=1"
	}
	return base + "/oauth-apps?oauth_error=1"
}

func (s *Service) storeTokens(ctx context.Context, orgID, accountID string, a *App, tok *oauth2.Token) error {
	if tok.AccessToken == "" {
		return errs.New(errs.CodeOauthExchangeFailed, "Provider returned no access token.")
	}
	accProvider, err := s.loadAccountProvider(ctx, orgID, accountID)
	if err != nil {
		return err
	}
	if !providersCompatible(accProvider, a.Provider) {
		return providerMismatch(accProvider, a.Provider)
	}
	access, err := s.Cipher.EncryptString(ctx, orgID, tok.AccessToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.accessToken", DocID: accountID,
	})
	if err != nil {
		return err
	}
	var refresh crypto.EncryptedString
	if tok.RefreshToken != "" {
		refresh, err = s.Cipher.EncryptString(ctx, orgID, tok.RefreshToken, crypto.AAD{
			Collection: accounts.Collection, Field: "oauth.refreshToken", DocID: accountID,
		})
		if err != nil {
			return err
		}
	}
	exp := tok.Expiry
	if exp.IsZero() {
		exp = s.clock().Add(time.Hour)
	}
	scopes := a.Scopes
	if sc, ok := tok.Extra("scope").(string); ok && sc != "" {
		scopes = strings.Fields(sc)
	}
	oauthDoc := AccountOAuth{
		AppID: a.ID, AccessToken: access, RefreshToken: refresh, ExpiresAt: exp.UTC(), Scopes: scopes,
	}
	now := s.clock().Truncate(time.Millisecond)
	filter := bson.M{"_id": accountID, "organizationId": orgID}
	update := bson.M{
		"$set": bson.M{
			"oauth": oauthDoc, "updatedAt": now,
			"stateReason": nil, "authFailingSince": nil,
		},
		"$inc": bson.M{"version": 1},
	}
	res, err := s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx, filter, update)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.account")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	_, _ = s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx,
		bson.M{"_id": accountID, "state": bson.M{"$in": []string{accounts.StateNew, accounts.StateAuthError}}},
		bson.M{"$set": bson.M{"state": accounts.StateConnecting}},
	)
	return nil
}

// Disconnect clears oauth on an account.
func (s *Service) Disconnect(ctx context.Context, accountID string) error {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	var doc struct {
		OAuth *AccountOAuth `bson:"oauth"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": org,
	}).Decode(&doc)
	if err != nil {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if doc.OAuth == nil || doc.OAuth.AppID == "" {
		return errs.New(errs.CodeOauthNotConnected, "The account is not connected with OAuth.")
	}
	if app, err := s.Repo.GetAppByID(ctx, org, doc.OAuth.AppID); err == nil {
		s.tryRevoke(ctx, accountID, app, doc.OAuth)
	}
	now := s.clock().Truncate(time.Millisecond)
	res, err := s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx,
		bson.M{"_id": accountID, "organizationId": org},
		bson.M{"$unset": bson.M{"oauth": ""}, "$set": bson.M{"updatedAt": now}, "$inc": bson.M{"version": 1}},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.disconnect")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	s.auditAccount(ctx, "account.oauth.disconnected", accountID, nil)
	return nil
}

func (s *Service) tryRevoke(ctx context.Context, accountID string, a *App, oa *AccountOAuth) {
	_, _, revokeURL := s.endpoints(a)
	if revokeURL == "" || oa.RefreshToken.IsZero() {
		return
	}
	rt, err := s.Cipher.DecryptString(ctx, oa.RefreshToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.refreshToken", DocID: accountID,
	})
	if err != nil {
		return
	}
	form := url.Values{"token": {rt}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
