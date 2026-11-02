// Package oauth owns the OAuth app registry, authorization-code mailbox connect,
// token refresh and connect links (P6.1).
package oauth

import (
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collections (S1 §6).
const (
	CollectionApps  = "oauthApps"
	CollectionLinks = "connectLinks"
)

// Providers.
const (
	ProviderGmail   = "gmail"
	ProviderOutlook = "outlook"
	ProviderMailru  = "mailru"
	ProviderGeneric = "generic"
)

// App is the stored oauthApps document.
type App struct {
	ID             string                 `bson:"_id" json:"id"`
	OrganizationID string                 `bson:"organizationId" json:"organizationId"`
	Name           string                 `bson:"name" json:"name"`
	Provider       string                 `bson:"provider" json:"provider"`
	ClientID       string                 `bson:"clientId" json:"clientId"`
	ClientSecret   crypto.EncryptedString `bson:"clientSecret" json:"-"`
	ServiceKey     crypto.EncryptedString `bson:"serviceKey,omitempty" json:"-"` // Gmail SA JSON (W38)
	TenantID       string                 `bson:"tenantId,omitempty" json:"tenantId,omitempty"`
	AppOnly        bool                   `bson:"appOnly,omitempty" json:"appOnly,omitempty"`
	AuthorizeURL   string                 `bson:"authorizeUrl,omitempty" json:"authorizeUrl,omitempty"`
	TokenURL       string                 `bson:"tokenUrl,omitempty" json:"tokenUrl,omitempty"`
	RevokeURL      string                 `bson:"revokeUrl,omitempty" json:"revokeUrl,omitempty"`
	Scopes         []string               `bson:"scopes,omitempty" json:"scopes,omitempty"`
	Enabled        bool                   `bson:"enabled" json:"enabled"`
	CreatedAt      time.Time              `bson:"createdAt" json:"createdAt"`
	UpdatedAt      time.Time              `bson:"updatedAt" json:"updatedAt"`
	Version        int64                  `bson:"version" json:"version"`
	CreatedBy      string                 `bson:"createdBy,omitempty" json:"-"`
}

// CreateInput is POST /v1/oauth-apps.
type CreateInput struct {
	Name         string   `json:"name" doc:"Display name, unique in the organisation (1–200 characters)." example:"Microsoft 365 – Acme"`
	Provider     string   `json:"provider" doc:"gmail, outlook, mailru or generic (any OAuth 2.0 server; needs authorizeUrl, tokenUrl and scopes). Immutable." example:"outlook"`
	ClientID     string   `json:"clientId" doc:"Client (application) ID registered at the provider (≤ 512 characters)." example:"3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13"`
	ClientSecret string   `json:"clientSecret" doc:"Client secret (required). Write-only: stored encrypted, never returned (responses show hasClientSecret)." example:"Qx8~vB2.mK7pL4sT9wR3yZ6nH1cF5dJ0gE"`
	ServiceKey   string   `json:"serviceKey,omitempty" doc:"Gmail only: Google service-account JSON key for domain-wide delegation (type service_account with client_email and private_key). Write-only." example:"{\"type\":\"service_account\",\"client_email\":\"mail-sync@acme-mail.iam.gserviceaccount.com\",\"private_key\":\"-----BEGIN PRIVATE KEY-----\\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC…\\n-----END PRIVATE KEY-----\\n\"}"`
	TenantID     string   `json:"tenantId,omitempty" doc:"Outlook only: Entra ID tenant (a GUID, common or organizations). Default common." example:"9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40"`
	AppOnly      *bool    `json:"appOnly,omitempty" doc:"Outlook only: client-credentials (app-only) app that opens mailboxes without user consent; accounts bind it with oauth.appId." example:"false"`
	AuthorizeURL string   `json:"authorizeUrl,omitempty" doc:"Authorization endpoint (generic providers; the presets fill it for the others)." example:"https://login.example.com/oauth2/authorize"`
	TokenURL     string   `json:"tokenUrl,omitempty" doc:"Token endpoint (generic providers; the presets fill it for the others)." example:"https://login.example.com/oauth2/token"`
	RevokeURL    string   `json:"revokeUrl,omitempty" doc:"Optional token revocation endpoint, called on disconnect." example:"https://login.example.com/oauth2/revoke"`
	Scopes       []string `json:"scopes,omitempty" doc:"Scopes to request; default: the provider preset (IMAP.AccessAsUser.All, SMTP.Send and offline_access for outlook, https://mail.google.com/ for gmail)." example:"[\"https://outlook.office.com/IMAP.AccessAsUser.All\",\"https://outlook.office.com/SMTP.Send\",\"offline_access\"]"`
	Enabled      *bool    `json:"enabled,omitempty" doc:"Whether connections may use the app. Default true." example:"true"`
}

// PatchInput is PATCH /v1/oauth-apps/{id}.
type PatchInput struct {
	Name         *string  `json:"name,omitempty" doc:"New display name (1–200 characters, unique)." example:"Microsoft 365 – Acme (prod)"`
	ClientID     *string  `json:"clientId,omitempty" doc:"New client ID." example:"3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13"`
	ClientSecret *string  `json:"clientSecret,omitempty" doc:"New client secret (write-only; replaces the stored one)." example:"Nf3~pQ8.aV5tW2kM7xS4bL9hJ6rC1eY0dG"`
	ServiceKey   *string  `json:"serviceKey,omitempty" doc:"New Gmail service-account JSON key (write-only)." example:"{\"type\":\"service_account\",\"client_email\":\"mail-sync@acme-mail.iam.gserviceaccount.com\",\"private_key\":\"-----BEGIN PRIVATE KEY-----\\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC…\\n-----END PRIVATE KEY-----\\n\"}"`
	TenantID     *string  `json:"tenantId,omitempty" doc:"New Entra ID tenant (GUID, common or organizations)." example:"organizations"`
	AppOnly      *bool    `json:"appOnly,omitempty" doc:"Switch between delegated (user consent) and app-only." example:"true"`
	AuthorizeURL *string  `json:"authorizeUrl,omitempty" doc:"New authorization endpoint." example:"https://login.example.com/oauth2/authorize"`
	TokenURL     *string  `json:"tokenUrl,omitempty" doc:"New token endpoint." example:"https://login.example.com/oauth2/token"`
	RevokeURL    *string  `json:"revokeUrl,omitempty" doc:"New revocation endpoint." example:"https://login.example.com/oauth2/revoke"`
	Scopes       []string `json:"scopes,omitempty" doc:"Replaces the requested scopes." example:"[\"https://mail.google.com/\"]"`
	Enabled      *bool    `json:"enabled,omitempty" doc:"false stops new connections and token refreshes with this app." example:"false"`
}

// View is the API representation (no secret).
type View struct {
	ID              string    `json:"id" doc:"OAuth app ID (oap_…)." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	OrganizationID  string    `json:"organizationId" doc:"Organisation that owns the app." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name            string    `json:"name" doc:"Display name." example:"Microsoft 365 – Acme"`
	Provider        string    `json:"provider" doc:"gmail, outlook, mailru or generic." example:"outlook"`
	ClientID        string    `json:"clientId" doc:"Client (application) ID." example:"3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13"`
	HasClientSecret bool      `json:"hasClientSecret" doc:"True when a client secret is stored (never returned)." example:"true"`
	HasServiceKey   bool      `json:"hasServiceKey" doc:"True when a Gmail service-account key is stored (never returned)." example:"false"`
	TenantID        string    `json:"tenantId,omitempty" doc:"Entra ID tenant (outlook)." example:"9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40"`
	AppOnly         bool      `json:"appOnly" doc:"True for an app-only / client-credentials app." example:"false"`
	AuthorizeURL    string    `json:"authorizeUrl,omitempty" doc:"Effective authorization endpoint (preset or configured)." example:"https://login.microsoftonline.com/9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40/oauth2/v2.0/authorize"`
	TokenURL        string    `json:"tokenUrl,omitempty" doc:"Effective token endpoint (preset or configured)." example:"https://login.microsoftonline.com/9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40/oauth2/v2.0/token"`
	RevokeURL       string    `json:"revokeUrl,omitempty" doc:"Revocation endpoint, when configured." example:"https://login.example.com/oauth2/revoke"`
	Scopes          []string  `json:"scopes,omitempty" doc:"Scopes requested on connect." example:"[\"https://outlook.office.com/IMAP.AccessAsUser.All\",\"https://outlook.office.com/SMTP.Send\",\"offline_access\"]"`
	Enabled         bool      `json:"enabled" doc:"Whether the app can be used." example:"true"`
	CreatedAt       time.Time `json:"createdAt" doc:"When the app was created." example:"2026-10-15T08:30:00Z"`
	UpdatedAt       time.Time `json:"updatedAt" doc:"Last change." example:"2026-10-22T17:05:12Z"`
	Version         int64     `json:"version" doc:"Resource version; the ETag is \"v\" followed by this number." example:"2"`
}

// ViewOf redacts an app.
func ViewOf(a *App) View {
	authURL, tokenURL, _ := resolveEndpoints(a)
	return View{
		ID: a.ID, OrganizationID: a.OrganizationID, Name: a.Name, Provider: a.Provider,
		ClientID: a.ClientID, HasClientSecret: !a.ClientSecret.IsZero(), HasServiceKey: !a.ServiceKey.IsZero(),
		TenantID: a.TenantID, AppOnly: a.AppOnly,
		AuthorizeURL: authURL, TokenURL: tokenURL, RevokeURL: a.RevokeURL,
		Scopes: a.Scopes, Enabled: a.Enabled, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version,
	}
}

// List is GET /v1/oauth-apps.
type List struct {
	Data        []View  `json:"data" doc:"The OAuth apps of this page."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6Im9hcF8wMW01cDI4ODY3YmYwMW1yYWt4MDhrMGFxcCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching apps (only with page and includeTotal)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

// ListFilter is the list query.
type ListFilter struct {
	Limit, Page  int
	Cursor, Sort string
	IncludeTotal bool
	Q            string
}

// VerifyResult is POST …/verify.
type VerifyResult struct {
	OK                 bool   `json:"ok" doc:"True when both endpoints answered." example:"true"`
	LatencyMs          int    `json:"latencyMs" doc:"Time the check took, in milliseconds." example:"184"`
	AuthorizeReachable *bool  `json:"authorizeReachable,omitempty" doc:"The authorization endpoint answered an HTTP request." example:"true"`
	TokenReachable     *bool  `json:"tokenReachable,omitempty" doc:"The token endpoint answered (an OAuth error for the dummy client counts as reachable)." example:"true"`
	Code               string `json:"code,omitempty" doc:"oauth_exchange_failed when ok is false." example:"oauth_exchange_failed"`
	Detail             string `json:"detail,omitempty" doc:"What failed, when ok is false." example:"One or more OAuth endpoints were unreachable."`
}

// StartInput is POST /v1/accounts/{id}/oauth/start.
type StartInput struct {
	AppID string `json:"appId" doc:"OAuth app (oap_…) to connect with; must be enabled and match the account provider." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
}

// StartResult is the authorize redirect payload.
type StartResult struct {
	AuthorizeURL string `json:"authorizeUrl" doc:"Provider consent page; send the user's browser there. After consent the provider redirects to /callbacks/{provider}." example:"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?client_id=3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13&code_challenge=Vq3sL0n9cY8xR2tM5pW7kH4jB6fD1gA0eZ3uN8iO2yE&code_challenge_method=S256&redirect_uri=https%3A%2F%2Femailbridge.example.com%2Fcallbacks%2Foutlook&response_type=code&scope=https%3A%2F%2Foutlook.office.com%2FIMAP.AccessAsUser.All+offline_access&state=Zk9mR2xQ7tV4wY1bN6cH8jL3"`
	State        string `json:"state" doc:"Opaque state bound to this account and app; single use, valid for 10 minutes." example:"Zk9mR2xQ7tV4wY1bN6cH8jL3"`
}

// ConnectLinkCreate is POST /v1/connect-links.
type ConnectLinkCreate struct {
	AccountID  string `json:"accountId" doc:"Account (acc_…) the mailbox owner will connect." example:"acc_01m5x5dtxq189hak49x3b25a5h"`
	AppID      string `json:"appId" doc:"OAuth app (oap_…) to use; must be enabled and match the account provider." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	TTLSeconds int    `json:"ttlSeconds,omitempty" doc:"Link lifetime in seconds, clamped to 60 – 604 800 (7 days). Default 3600." example:"86400"`
}

// ConnectLinkCreated is the one-time response.
type ConnectLinkCreated struct {
	ID        string    `json:"id" doc:"Connect link ID (lnk_…)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm"`
	URL       string    `json:"url" doc:"One-time link to send to the mailbox owner. Shown only in this response (only a hash is stored)." example:"https://emailbridge.example.com/connect/lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
	ExpiresAt time.Time `json:"expiresAt" doc:"When the link stops working." example:"2026-10-28T09:30:00Z"`
}

// ConnectLink is the stored document.
type ConnectLink struct {
	ID             string     `bson:"_id"`
	OrganizationID string     `bson:"organizationId"`
	AccountID      string     `bson:"accountId"`
	AppID          string     `bson:"appId"`
	Mode           string     `bson:"mode,omitempty"` // "hosted" links belong to the hosted page, never to redeem
	TokenHash      string     `bson:"tokenHash"`
	ExpiresAt      time.Time  `bson:"expiresAt"`
	UsedAt         *time.Time `bson:"usedAt,omitempty"`
	CreatedAt      time.Time  `bson:"createdAt"`
	CreatedBy      string     `bson:"createdBy,omitempty"`
}

// Auth modes for accounts.oauth.mode (W43). Empty = user-consent (P6.1).
const (
	AuthModeServiceAccount = "serviceAccount"
	AuthModeAppOnly        = "appOnly"
)

// AccountOAuth is accounts.oauth (S1).
type AccountOAuth struct {
	AppID        string                 `bson:"appId"`
	Mode         string                 `bson:"mode,omitempty"` // W43: serviceAccount | appOnly
	RefreshToken crypto.EncryptedString `bson:"refreshToken"`
	AccessToken  crypto.EncryptedString `bson:"accessToken"`
	ExpiresAt    time.Time              `bson:"expiresAt"`
	Scopes       []string               `bson:"scopes,omitempty"`
}

// AccountOAuthView is the redacted API form.
type AccountOAuthView struct {
	AppID     string    `json:"appId" doc:"OAuth app the account is connected with; empty when not connected." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	Mode      string    `json:"mode,omitempty" doc:"Absent for user consent; serviceAccount or appOnly for apps that need no consent." example:"appOnly"`
	ExpiresAt time.Time `json:"expiresAt" doc:"Expiry of the current access token (refreshed automatically before it expires); zero time when not connected." example:"2026-10-27T10:31:07Z"`
	Scopes    []string  `json:"scopes,omitempty" doc:"Scopes granted for the account." example:"[\"https://outlook.office.com/IMAP.AccessAsUser.All\",\"https://outlook.office.com/SMTP.Send\",\"offline_access\"]"`
	Connected bool      `json:"connected" doc:"True when OAuth tokens are stored for the account." example:"true"`
}

// ViewOfAccountOAuth redacts tokens.
func ViewOfAccountOAuth(o *AccountOAuth) AccountOAuthView {
	if o == nil || o.AppID == "" {
		return AccountOAuthView{}
	}
	return AccountOAuthView{
		AppID: o.AppID, Mode: o.Mode, ExpiresAt: o.ExpiresAt, Scopes: o.Scopes, Connected: true,
	}
}

type preset struct {
	AuthorizeURL, TokenURL, RevokeURL string
	Scopes                            []string
}

//nolint:gosec // G101: provider endpoint URLs, not credentials.
var presets = map[string]preset{
	ProviderGmail: {
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		RevokeURL:    "https://oauth2.googleapis.com/revoke",
		Scopes:       []string{"https://mail.google.com/"},
	},
	ProviderOutlook: {
		AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes: []string{
			"https://outlook.office.com/IMAP.AccessAsUser.All",
			"https://outlook.office.com/SMTP.Send",
			"offline_access",
		},
	},
	ProviderMailru: {
		AuthorizeURL: "https://oauth.mail.ru/login",
		TokenURL:     "https://oauth.mail.ru/token",
		Scopes:       []string{"userinfo", "mail.imap"},
	},
}
