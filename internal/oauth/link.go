package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/providers/gmail"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// saTokenTTL is the cache lifetime when Gmail SA minting does not return expires_in (typically 3600s).
const saTokenTTL = 55 * time.Minute

// LinkAppOnly binds an account to an OAuth app without user consent (W43).
// Gmail + serviceKey → mode serviceAccount; graph account + outlook appOnly → mode appOnly.
func (s *Service) LinkAppOnly(ctx context.Context, accountID, appID string, skipVerify bool) error {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if !store.IsID(store.PrefixOAuthApp, appID) {
		return errs.New(errs.CodeOauthAppLinkInvalid, "oauth.appId must be a valid OAuth app id.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	org := c.OrganizationID
	if org == "" {
		return errs.New(errs.CodeForbidden, "An organisation context is required.")
	}

	acc, err := s.loadAccountForLink(ctx, org, accountID)
	if err != nil {
		return err
	}
	app, err := s.Repo.getApp(ctx, appID) // org-scoped; missing / other-org → not_found
	if err != nil {
		return err
	}
	if !app.Enabled {
		return errs.New(errs.CodeOauthAppDisabled, "The OAuth app is disabled.")
	}
	mode, err := resolveLinkMode(acc.Provider, app)
	if err != nil {
		return err
	}

	subject := strings.TrimSpace(acc.Email)
	if subject == "" {
		return errs.New(errs.CodeValidationFailed, "Account email is required for service-account / app-only linking.")
	}

	now := s.clock().Truncate(time.Millisecond)
	oauthDoc := AccountOAuth{AppID: app.ID, Mode: mode, Scopes: append([]string(nil), app.Scopes...)}
	set := bson.M{
		"oauth": oauthDoc, "updatedAt": now,
		"stateReason": nil, "authFailingSince": nil,
	}
	if strings.TrimSpace(acc.IMAPUser) == "" {
		set["imap.auth.user"] = subject
	}
	if strings.TrimSpace(acc.SMTPUser) == "" {
		set["smtp.auth.user"] = subject
	}
	if _, err := s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx,
		bson.M{"_id": accountID, "organizationId": org},
		bson.M{"$set": set, "$inc": bson.M{"version": 1}},
	); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.link")
	}

	if skipVerify {
		s.audit(ctx, "account.oauth.link", accountID, nil)
		return nil
	}
	if err := s.verifyAppOnly(ctx, org, accountID, subject, app, mode); err != nil {
		_, _ = s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx,
			bson.M{"_id": accountID, "organizationId": org},
			bson.M{
				"$unset": bson.M{"oauth": ""},
				"$inc":   bson.M{"version": 1},
				"$set":   bson.M{"updatedAt": s.clock().Truncate(time.Millisecond)},
			},
		)
		return err
	}
	s.audit(ctx, "account.oauth.link", accountID, nil)
	return nil
}

type linkAccount struct {
	ID       string
	Email    string
	Provider string
	IMAPUser string
	SMTPUser string
}

func (s *Service) loadAccountForLink(ctx context.Context, org, accountID string) (*linkAccount, error) {
	var doc struct {
		ID       string `bson:"_id"`
		Email    string `bson:"email"`
		Provider string `bson:"provider"`
		IMAP     struct {
			Auth struct {
				User string `bson:"user"`
			} `bson:"auth"`
		} `bson:"imap"`
		SMTP struct {
			Auth struct {
				User string `bson:"user"`
			} `bson:"auth"`
		} `bson:"smtp"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": org,
	}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return &linkAccount{
		ID: doc.ID, Email: doc.Email, Provider: doc.Provider,
		IMAPUser: doc.IMAP.Auth.User, SMTPUser: doc.SMTP.Auth.User,
	}, nil
}

func resolveLinkMode(accountProvider string, app *App) (string, error) {
	ap := strings.ToLower(strings.TrimSpace(accountProvider))
	if ap == "" {
		ap = accounts.ProviderIMAP
	}
	switch ap {
	case accounts.ProviderGmail:
		if app.Provider != ProviderGmail {
			return "", errs.New(errs.CodeOauthAppLinkInvalid,
				"Gmail accounts require a gmail OAuth app with a service-account key.").WithSafe("appProvider", app.Provider)
		}
		if app.ServiceKey.IsZero() {
			return "", errs.New(errs.CodeOauthAppLinkInvalid,
				"The Gmail OAuth app has no service-account key; use the user-consent connect flow instead.")
		}
		return AuthModeServiceAccount, nil
	case accounts.ProviderGraph:
		if app.Provider != ProviderOutlook {
			return "", errs.New(errs.CodeOauthAppLinkInvalid,
				"Graph accounts require an outlook OAuth app with appOnly enabled.").WithSafe("appProvider", app.Provider)
		}
		if !app.AppOnly {
			return "", errs.New(errs.CodeOauthAppLinkInvalid,
				"The Outlook OAuth app is not app-only; use the user-consent connect flow instead.")
		}
		return AuthModeAppOnly, nil
	default:
		return "", errs.New(errs.CodeOauthAppLinkInvalid,
			"oauth.appId linking without consent is only supported for gmail and graph accounts.").WithSafe("provider", ap)
	}
}

func (s *Service) verifyAppOnly(ctx context.Context, org, accountID, subject string, app *App, mode string) error {
	tok, _, exp, err := s.mintAppOnly(ctx, app, mode, subject)
	if err != nil {
		return err
	}
	if err := s.probeAppOnly(ctx, mode, subject, tok); err != nil {
		return err
	}
	oauthTok := &oauth2.Token{AccessToken: tok, Expiry: exp}
	return s.storeAppOnlyTokens(ctx, org, accountID, app, mode, oauthTok)
}

func (s *Service) mintAppOnly(ctx context.Context, app *App, mode, subject string) (token, username string, exp time.Time, err error) {
	switch mode {
	case AuthModeServiceAccount:
		raw, err := s.DecryptServiceKey(ctx, app)
		if err != nil {
			return "", "", time.Time{}, err
		}
		tokenURL := ""
		var keyMeta struct {
			TokenURI string `json:"token_uri"`
		}
		_ = json.Unmarshal([]byte(raw), &keyMeta)
		if keyMeta.TokenURI != "" {
			tokenURL = keyMeta.TokenURI
		}
		if app.TokenURL != "" {
			tokenURL = app.TokenURL
		}
		src, err := gmail.NewServiceAccountSourceFromJSON([]byte(raw), subject, &gmail.Client{TokenURL: tokenURL})
		if err != nil {
			return "", "", time.Time{}, err
		}
		src.HTTP = s.httpClient()
		tok, user, err := src.AccessToken(ctx, "")
		if err != nil {
			return "", "", time.Time{}, err
		}
		return tok, user, s.clock().Add(saTokenTTL), nil
	case AuthModeAppOnly:
		mat, err := s.ClientCredentialsMaterial(ctx, app)
		if err != nil {
			return "", "", time.Time{}, err
		}
		tokenURL := mat.TokenURL()
		if app.TokenURL != "" {
			tokenURL = app.TokenURL
		}
		creds := &graph.ClientCredentials{
			HTTP: s.httpClient(), TokenURL: tokenURL, TenantID: mat.TenantID,
			ClientID: mat.ClientID, Secret: mat.Secret,
		}
		tok, exp, err := creds.Token(ctx)
		if err != nil {
			return "", "", time.Time{}, err
		}
		return tok, subject, exp, nil
	default:
		return "", "", time.Time{}, errs.New(errs.CodeOauthAppLinkInvalid, "Unknown OAuth link mode.")
	}
}

func (s *Service) probeAppOnly(ctx context.Context, mode, subject, token string) error {
	hc := s.httpClient()
	switch mode {
	case AuthModeServiceAccount:
		base := s.GmailAPIRoot
		c := &gmail.Client{HTTP: hc, BaseURL: base}
		prof, err := c.GetProfile(ctx, token)
		if err != nil {
			return errs.Wrap(err, errs.CodeOauthAppLinkInvalid, "Gmail profile probe failed.")
		}
		if !strings.EqualFold(strings.TrimSpace(prof.EmailAddress), strings.TrimSpace(subject)) {
			return errs.New(errs.CodeOauthAppLinkInvalid,
				"Service-account impersonation email does not match the account email.").
				WithSafe("expected", subject)
		}
		return nil
	case AuthModeAppOnly:
		base := s.GraphAPIRoot
		c := &graph.Client{HTTP: hc, BaseURL: base, UPN: subject}
		if _, err := c.ListFolders(ctx, token); err != nil {
			return errs.Wrap(err, errs.CodeOauthAppLinkInvalid, "Graph mailbox probe failed.")
		}
		return nil
	default:
		return errs.New(errs.CodeOauthAppLinkInvalid, "Unknown OAuth link mode.")
	}
}

func (s *Service) storeAppOnlyTokens(ctx context.Context, orgID, accountID string, a *App, mode string, tok *oauth2.Token) error {
	if tok.AccessToken == "" {
		return errs.New(errs.CodeOauthExchangeFailed, "Provider returned no access token.")
	}
	access, err := s.Cipher.EncryptString(ctx, orgID, tok.AccessToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.accessToken", DocID: accountID,
	})
	if err != nil {
		return err
	}
	exp := tok.Expiry
	if exp.IsZero() {
		exp = s.clock().Add(saTokenTTL)
	}
	oauthDoc := AccountOAuth{
		AppID: a.ID, Mode: mode, AccessToken: access, ExpiresAt: exp.UTC(), Scopes: a.Scopes,
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

// ensureAppOnlyStillValid loads the app and checks SA / appOnly prerequisites; on failure marks authError.
func (s *Service) ensureAppOnlyStillValid(ctx context.Context, doc *accountOAuthDoc) (*App, error) {
	if doc.OAuth == nil {
		return nil, errs.New(errs.CodeOauthNotConnected, "The account is not connected with OAuth.")
	}
	app, err := s.Repo.GetAppByID(ctx, doc.OrganizationID, doc.OAuth.AppID)
	if err != nil {
		return nil, s.markAppOnlyRevoked(ctx, doc, "OAuth app was deleted or is unavailable.")
	}
	if !app.Enabled {
		return nil, s.markAppOnlyRevoked(ctx, doc, "OAuth app is disabled.")
	}
	switch doc.OAuth.Mode {
	case AuthModeServiceAccount:
		if app.ServiceKey.IsZero() {
			return nil, s.markAppOnlyRevoked(ctx, doc, "Service-account key was removed from the OAuth app.")
		}
	case AuthModeAppOnly:
		if !app.AppOnly {
			return nil, s.markAppOnlyRevoked(ctx, doc, "App-only was turned off on the OAuth app.")
		}
	}
	return app, nil
}

func (s *Service) markAppOnlyRevoked(ctx context.Context, doc *accountOAuthDoc, message string) error {
	now := s.clock().Truncate(time.Millisecond)
	reason := accounts.StateReason{
		Kind: string(errs.KindAuth), Code: string(errs.CodeOauthRevoked),
		Message: message, At: now,
	}
	_, err := s.Repo.DB.Collection(accounts.Collection).UpdateOne(ctx,
		bson.M{"_id": doc.ID},
		bson.M{
			"$set": bson.M{
				"state": accounts.StateAuthError, "stateReason": reason,
				"updatedAt": now, "oauth.accessToken": crypto.EncryptedString{},
				"oauth.refreshToken": crypto.EncryptedString{},
			},
			"$inc": bson.M{"version": 1},
		},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.revoke")
	}
	return errs.New(errs.CodeOauthRevoked, message)
}

func (s *Service) accessTokenAppOnly(ctx context.Context, doc *accountOAuthDoc, username string) (token, user string, err error) {
	if username == "" {
		var emailDoc struct {
			Email string `bson:"email"`
		}
		_ = s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": doc.ID}).Decode(&emailDoc)
		username = emailDoc.Email
	}
	user = username
	if doc.OAuth.ExpiresAt.After(s.clock().Add(refreshSkew)) && !doc.OAuth.AccessToken.IsZero() {
		tok, err := s.Cipher.DecryptString(ctx, doc.OAuth.AccessToken, crypto.AAD{
			Collection: accounts.Collection, Field: "oauth.accessToken", DocID: doc.ID,
		})
		if err != nil {
			return "", "", errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
		}
		return tok, user, nil
	}
	if err := s.mintAppOnlyWithLease(ctx, doc, username); err != nil {
		return "", "", err
	}
	doc, err = s.loadAccountOAuth(ctx, doc.ID)
	if err != nil {
		return "", "", err
	}
	if doc.OAuth == nil || doc.OAuth.AccessToken.IsZero() {
		return "", "", errs.New(errs.CodeOauthRevoked, "OAuth tokens were revoked.")
	}
	tok, err := s.Cipher.DecryptString(ctx, doc.OAuth.AccessToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.accessToken", DocID: doc.ID,
	})
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	return tok, user, nil
}

func (s *Service) mintAppOnlyWithLease(ctx context.Context, doc *accountOAuthDoc, subject string) error {
	key := leaseKeyPref + doc.ID
	node := s.NodeID
	if node == "" {
		node = "local"
	}
	doMint := func() error {
		app, err := s.ensureAppOnlyStillValid(ctx, doc)
		if err != nil {
			return err
		}
		tok, _, exp, err := s.mintAppOnly(ctx, app, doc.OAuth.Mode, subject)
		if err != nil {
			return err
		}
		return s.storeAppOnlyTokens(ctx, doc.OrganizationID, doc.ID, app, doc.OAuth.Mode, &oauth2.Token{
			AccessToken: tok, Expiry: exp,
		})
	}
	if s.Leases == nil {
		return doMint()
	}
	for i := 0; i < refreshTries; i++ {
		epoch, err := s.Leases.Claim(ctx, key, []byte(node), refreshLease)
		if err == nil {
			defer func() { _ = s.Leases.Release(context.WithoutCancel(ctx), key, epoch) }()
			return doMint()
		}
		if !errors.Is(err, bus.ErrLeaseHeld) {
			return errs.Wrap(err, errs.CodeInternalError, "oauth.lease")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(refreshWait):
		}
		fresh, err := s.loadAccountOAuth(ctx, doc.ID)
		if err != nil {
			return err
		}
		if fresh.OAuth != nil && fresh.OAuth.ExpiresAt.After(s.clock().Add(refreshSkew)) && !fresh.OAuth.AccessToken.IsZero() {
			return nil
		}
		doc = fresh
	}
	return errs.New(errs.CodeServiceUnavailable, "Could not acquire OAuth refresh lease.")
}
