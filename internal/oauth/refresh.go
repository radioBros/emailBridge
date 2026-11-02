package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	refreshSkew  = 2 * time.Minute
	refreshLease = 30 * time.Second
	refreshWait  = 200 * time.Millisecond
	refreshTries = 15
	leaseKeyPref = "oauth.refresh."
)

type accountOAuthDoc struct {
	ID             string        `bson:"_id"`
	OrganizationID string        `bson:"organizationId"`
	OAuth          *AccountOAuth `bson:"oauth"`
	Delegated      *struct {
		Account string `bson:"account"`
		User    string `bson:"user"`
	} `bson:"delegated"`
	IMAP struct {
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

// AccessToken returns a valid access token for the account, refreshing under a cluster lease when needed.
// Service-account / app-only accounts (W43) mint via JWT bearer / client_credentials instead of refresh_token.
// Delegated accounts (W54) use the parent account's tokens with the shared mailbox as username.
func (s *Service) AccessToken(ctx context.Context, accountID string) (token, username string, err error) {
	doc, err := s.loadAccountOAuth(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	if doc.Delegated != nil && doc.Delegated.Account != "" {
		// One level only: a delegated account must authenticate through an account with its own OAuth link
		// (a chain or a loop of delegations would recurse without end).
		parent, err := s.loadAccountOAuth(ctx, doc.Delegated.Account)
		if err != nil {
			return "", "", err
		}
		if parent.OrganizationID != doc.OrganizationID {
			// Validated on create / update too; re-checked here so a moved account never borrows a foreign token.
			return "", "", errs.New(errs.CodeOauthNotConnected, "The delegating account belongs to another organisation.")
		}
		if parent.Delegated != nil && parent.Delegated.Account != "" {
			return "", "", errs.New(errs.CodeOauthNotConnected, "The delegating account is itself delegated; delegation chains are not supported.")
		}
		tok, _, err := s.AccessToken(ctx, doc.Delegated.Account)
		if err != nil {
			return "", "", err
		}
		user := strings.TrimSpace(doc.Delegated.User)
		if user == "" {
			user = doc.IMAP.Auth.User
		}
		return tok, user, nil
	}
	if doc.OAuth == nil || doc.OAuth.AppID == "" {
		return "", "", errs.New(errs.CodeOauthNotConnected, "The account is not connected with OAuth.")
	}
	username = doc.IMAP.Auth.User
	if username == "" {
		username = doc.SMTP.Auth.User
	}
	if doc.OAuth.Mode == AuthModeServiceAccount || doc.OAuth.Mode == AuthModeAppOnly {
		return s.accessTokenAppOnly(ctx, doc, username)
	}
	if doc.OAuth.ExpiresAt.After(s.clock().Add(refreshSkew)) && !doc.OAuth.AccessToken.IsZero() {
		tok, err := s.Cipher.DecryptString(ctx, doc.OAuth.AccessToken, crypto.AAD{
			Collection: accounts.Collection, Field: "oauth.accessToken", DocID: accountID,
		})
		if err != nil {
			return "", "", errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
		}
		return tok, username, nil
	}
	if err := s.refreshWithLease(ctx, doc); err != nil {
		return "", "", err
	}
	doc, err = s.loadAccountOAuth(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	if doc.OAuth == nil || doc.OAuth.AccessToken.IsZero() {
		return "", "", errs.New(errs.CodeOauthRevoked, "OAuth tokens were revoked.")
	}
	tok, err := s.Cipher.DecryptString(ctx, doc.OAuth.AccessToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.accessToken", DocID: accountID,
	})
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	return tok, username, nil
}

func (s *Service) loadAccountOAuth(ctx context.Context, accountID string) (*accountOAuthDoc, error) {
	var doc accountOAuthDoc
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": accountID}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return &doc, nil
}

func (s *Service) refreshWithLease(ctx context.Context, doc *accountOAuthDoc) error {
	key := leaseKeyPref + doc.ID
	node := s.NodeID
	if node == "" {
		node = "local"
	}
	for i := 0; i < refreshTries; i++ {
		if s.Leases == nil {
			return s.doRefresh(ctx, doc)
		}
		epoch, err := s.Leases.Claim(ctx, key, []byte(node), refreshLease)
		if err == nil {
			defer func() { _ = s.Leases.Release(context.WithoutCancel(ctx), key, epoch) }()
			return s.doRefresh(ctx, doc)
		}
		if !errors.Is(err, bus.ErrLeaseHeld) {
			return errs.Wrap(err, errs.CodeInternalError, "oauth.lease")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(refreshWait):
		}
		// Re-read: another node may have refreshed.
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

func (s *Service) doRefresh(ctx context.Context, doc *accountOAuthDoc) error {
	if doc.OAuth == nil || doc.OAuth.RefreshToken.IsZero() {
		return s.markRevoked(ctx, doc, "missing refresh token")
	}
	app, err := s.Repo.GetAppByID(ctx, doc.OrganizationID, doc.OAuth.AppID)
	if err != nil {
		return err
	}
	secret, err := s.decryptSecret(ctx, app)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	rt, err := s.Cipher.DecryptString(ctx, doc.OAuth.RefreshToken, crypto.AAD{
		Collection: accounts.Collection, Field: "oauth.refreshToken", DocID: doc.ID,
	})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	_, tokenURL, _ := s.endpoints(app)
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rt},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.refresh")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return errs.Wrap(err, errs.CodeOauthExchangeFailed, "oauth.refresh")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &er)
		if er.Error == "invalid_grant" || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "invalid_grant") {
			return s.markRevoked(ctx, doc, er.Error)
		}
		return errs.New(errs.CodeOauthExchangeFailed, "OAuth token refresh failed.").WithSafe("status", resp.StatusCode)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return errs.New(errs.CodeOauthExchangeFailed, "OAuth token refresh returned an invalid body.")
	}
	oauthTok := &oauth2.Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       s.clock().Add(time.Duration(tok.ExpiresIn) * time.Second),
	}
	if tok.ExpiresIn <= 0 {
		oauthTok.Expiry = s.clock().Add(time.Hour)
	}
	if oauthTok.RefreshToken == "" {
		oauthTok.RefreshToken = rt // keep existing refresh token
	}
	if tok.Scope != "" {
		oauthTok = oauthTok.WithExtra(map[string]any{"scope": tok.Scope})
	}
	return s.storeTokens(ctx, doc.OrganizationID, doc.ID, app, oauthTok)
}

func (s *Service) markRevoked(ctx context.Context, doc *accountOAuthDoc, detail string) error {
	now := s.clock().Truncate(time.Millisecond)
	reason := accounts.StateReason{
		Kind: string(errs.KindAuth), Code: string(errs.CodeOauthRevoked),
		Message: "OAuth grant was revoked or expired.", At: now,
	}
	if detail != "" && detail != "invalid_grant" {
		reason.Message = "OAuth grant was revoked."
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
	return errs.New(errs.CodeOauthRevoked, "OAuth grant was revoked.")
}
