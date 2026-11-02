package oauth

import (
	"context"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	tokenExportRatePerHour = 30
	featureTokenExport     = "oauth." + "tokenExport"
)

// GetAccountOAuth returns the redacted OAuth view for an account (W54 G22).
func (s *Service) GetAccountOAuth(ctx context.Context, accountID string) (AccountOAuthView, error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", accountID); err != nil {
		return AccountOAuthView{}, err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return AccountOAuthView{}, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return AccountOAuthView{}, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	var doc struct {
		OAuth *AccountOAuth `bson:"oauth"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": org,
	}).Decode(&doc)
	if err != nil {
		return AccountOAuthView{}, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	v := ViewOfAccountOAuth(doc.OAuth)
	if !v.Connected {
		v.Connected = false
	}
	return v, nil
}

// TokenExport is GET â€¦/oauth-token (W54 G21).
type TokenExport struct {
	AccessToken string    `json:"accessToken" doc:"Current OAuth access token, for IMAP/SMTP XOAUTH2 or the provider API. Treat it as a secret and never log it." example:"eyJ0eXAiOiJKV1QiLCJub25jZSI6IkFRQUJBQUFBIiwiYWxnIjoiUlMyNTYifQ.eyJhdWQiOiJodHRwczovL291dGxvb2sub2ZmaWNlLmNvbSJ9.c2lnbmF0dXJl"`
	TokenType   string    `json:"tokenType" doc:"Always Bearer." example:"Bearer"`
	ExpiresAt   time.Time `json:"expiresAt" doc:"When the token expires; request a new one after that." example:"2026-10-27T10:31:07Z"`
	Username    string    `json:"username,omitempty" doc:"Mailbox user name to pair with the token in XOAUTH2." example:"sales@example.org"`
}

// ExportAccessToken returns a short-lived access token when oauth.tokenExport is on.
func (s *Service) ExportAccessToken(ctx context.Context, accountID string) (*TokenExport, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermOauthTokenExport, accountID); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	acc, err := s.loadAccountForExport(ctx, org, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.requireTokenExport(ctx, org, acc.Switches); err != nil {
		return nil, err
	}
	if err := s.takeTokenExportRate(accountID); err != nil {
		return nil, err
	}
	tok, user, err := s.AccessToken(ctx, accountID)
	if err != nil {
		return nil, err
	}
	exp := s.clock().Add(time.Hour)
	appID := ""
	if acc.OAuth != nil {
		appID = acc.OAuth.AppID
		if !acc.OAuth.ExpiresAt.IsZero() {
			exp = acc.OAuth.ExpiresAt
		}
	}
	s.auditAccount(ctx, "account.oauth.token_export", accountID, audit.Changes{
		"appId": {After: appID},
	})
	return &TokenExport{
		AccessToken: tok, TokenType: "Bearer", ExpiresAt: exp.UTC(), Username: user,
	}, nil
}

type exportAccount struct {
	Switches map[string]any
	OAuth    *AccountOAuth
}

func (s *Service) loadAccountForExport(ctx context.Context, org, accountID string) (*exportAccount, error) {
	var doc struct {
		Switches map[string]any `bson:"switches"`
		OAuth    *AccountOAuth  `bson:"oauth"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": org,
	}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return &exportAccount{Switches: doc.Switches, OAuth: doc.OAuth}, nil
}

func (s *Service) requireTokenExport(ctx context.Context, orgID string, accountSwitches map[string]any) error {
	reg := features.DefaultRegistry()
	sw, ok := reg.Get(featureTokenExport)
	if !ok {
		return errs.New(errs.CodeFeatureDisabled, "oauth.tokenExport is disabled.")
	}
	vals := features.Values{}
	if accountSwitches != nil {
		if v, ok := accountSwitches[featureTokenExport].(bool); ok {
			vals.Lower = &features.Setting{Value: features.BoolValue(v)}
		}
	}
	read := func(docID string) *features.Setting {
		var doc settings.Doc
		err := s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
		if err != nil || doc.Switches == nil {
			return nil
		}
		if sv, ok := doc.Switches[featureTokenExport]; ok {
			if b, ok := sv.Value.(bool); ok {
				return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
		return nil
	}
	if features.Allows(sw, features.ScopeGlobal) {
		vals.Global = read(settings.DocIDGlobal)
	}
	vals.Organization = read(settings.OrgDocID(orgID))
	v, _ := features.Resolve(sw, "production", vals)
	if v.Bool == nil || !*v.Bool {
		return errs.New(errs.CodeFeatureDisabled, "oauth.tokenExport is disabled for this account.")
	}
	return nil
}

type exportBucket struct {
	tokens float64
	last   time.Time
}

var (
	exportRateMu sync.Mutex
	exportRate   = map[string]*exportBucket{}
)

func (s *Service) takeTokenExportRate(accountID string) error {
	now := s.clock()
	exportRateMu.Lock()
	defer exportRateMu.Unlock()
	b, ok := exportRate[accountID]
	if !ok {
		b = &exportBucket{tokens: tokenExportRatePerHour, last: now}
		exportRate[accountID] = b
	}
	elapsed := now.Sub(b.last).Hours()
	b.tokens += elapsed * tokenExportRatePerHour
	if b.tokens > tokenExportRatePerHour {
		b.tokens = tokenExportRatePerHour
	}
	b.last = now
	if b.tokens < 1 {
		return errs.New(errs.CodeRateLimited, "OAuth token export rate limit exceeded.")
	}
	b.tokens--
	return nil
}
