package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultLinkTTL = time.Hour
	maxLinkTTL     = 7 * 24 * time.Hour
	minLinkTTL     = time.Minute
)

// CreateConnectLink mints a one-time signed connect link.
func (s *Service) CreateConnectLink(ctx context.Context, in ConnectLinkCreate) (*ConnectLinkCreated, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, in.AccountID) {
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
	accProvider, err := s.loadAccountProvider(ctx, org, in.AccountID)
	if err != nil {
		return nil, err
	}
	if !providersCompatible(accProvider, a.Provider) {
		return nil, providerMismatch(accProvider, a.Provider)
	}
	ttl := defaultLinkTTL
	if in.TTLSeconds > 0 {
		ttl = time.Duration(in.TTLSeconds) * time.Second
	}
	if ttl < minLinkTTL {
		ttl = minLinkTTL
	}
	if ttl > maxLinkTTL {
		ttl = maxLinkTTL
	}
	secret, err := randomURLString(18)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.link")
	}
	id := store.NewID(store.PrefixConnectLink)
	sum := sha256.Sum256([]byte(id + "." + secret))
	now := s.clock().Truncate(time.Millisecond)
	link := &ConnectLink{
		ID: id, OrganizationID: org, AccountID: in.AccountID, AppID: in.AppID,
		TokenHash: hex.EncodeToString(sum[:]), ExpiresAt: now.Add(ttl), CreatedAt: now,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		link.CreatedBy = c.UserID
		if c.Kind == tenancy.CallerAPIKey {
			link.CreatedBy = c.APIKeyID
		}
	}
	if err := s.Repo.insertLink(ctx, link); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.link")
	}
	url := strings.TrimRight(s.PublicURL, "/") + "/connect/" + id + "." + secret
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "connectLink.created", audit.Target{Type: "connectLink", ID: id}, audit.Changes{
			"accountId": {After: in.AccountID}, "appId": {After: in.AppID},
		})
	}
	return &ConnectLinkCreated{ID: id, URL: url, ExpiresAt: link.ExpiresAt}, nil
}

// RedeemConnectLink validates a public token and starts OAuth (302 to provider).
func (s *Service) RedeemConnectLink(ctx context.Context, w http.ResponseWriter, r *http.Request, token string) {
	id, secret, ok := splitConnectToken(token)
	if !ok {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired."))
		return
	}
	link, err := s.Repo.getLink(ctx, id)
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	if link.UsedAt != nil || link.ExpiresAt.Before(s.clock()) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired."))
		return
	}
	sum := sha256.Sum256([]byte(id + "." + secret))
	if !subtleConstantTimeEqual(hex.EncodeToString(sum[:]), link.TokenHash) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired."))
		return
	}
	// Hosted-page links (no account yet) are redeemed only through the hosted page (connect.hostedPage).
	if link.Mode == "hosted" || !store.IsID(store.PrefixAccount, link.AccountID) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired."))
		return
	}
	// Check the app and build the authorize URL before burning the single-use link, so a disabled app or a
	// state-store outage does not consume it.
	a, err := s.Repo.GetAppByID(ctx, link.OrganizationID, link.AppID)
	if err != nil || !a.Enabled {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeOauthAppDisabled, "The OAuth app is disabled."))
		return
	}
	res, err := s.buildAuthorize(ctx, link.OrganizationID, link.AccountID, a, "", "")
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	if err := s.Repo.markLinkUsed(ctx, id, s.clock().Truncate(time.Millisecond)); err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	http.Redirect(w, r, res.AuthorizeURL, http.StatusFound)
}

func splitConnectToken(token string) (id, secret string, ok bool) {
	token = strings.TrimSpace(token)
	i := strings.IndexByte(token, '.')
	if i <= 0 || i >= len(token)-1 {
		return "", "", false
	}
	id, secret = token[:i], token[i+1:]
	if !store.IsID(store.PrefixConnectLink, id) || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func subtleConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
