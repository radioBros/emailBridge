// Package connect owns the hosted connect page and identity pinning (P6.4).
package connect

import (
	"time"

	"github.com/radiobros/emailbridge/internal/autoconfig"
)

const (
	CollectionLinks  = "connectLinks"
	CollectionAssets = "brandingAssets"
	ModeHosted       = "hosted"
	ModeOAuth        = "oauth"
	switchHosted     = "connect.hostedPage"
	switchAntivirus  = "attachments.antivirus"
	csrfTTL          = 30 * time.Minute
	defaultLinkTTL   = time.Hour
	maxLinkTTL       = 7 * 24 * time.Hour
	minLinkTTL       = time.Minute
	maxAssetsPerOrg  = 50
	maxAssetsBytes   = 20 << 20
)

// AssetKind is a branding upload category.
type AssetKind string

// Asset kinds.
const (
	AssetKindLogo     AssetKind = "logo"
	AssetKindLogoDark AssetKind = "logoDark"
	AssetKindFavicon  AssetKind = "favicon"
	AssetKindImage    AssetKind = "image"
)

// Valid reports whether k is a known asset kind.
func (k AssetKind) Valid() bool {
	switch k {
	case AssetKindLogo, AssetKindLogoDark, AssetKindFavicon, AssetKindImage:
		return true
	}
	return false
}

// Link is a connectLinks document (extends P6.1).
type Link struct {
	ID             string `bson:"_id"`
	OrganizationID string `bson:"organizationId"`
	AccountID      string `bson:"accountId,omitempty"`
	AppID          string `bson:"appId,omitempty"`
	Mode           string `bson:"mode,omitempty"`
	ReturnURL      string `bson:"returnUrl,omitempty"`
	// PendingAccountID is the account a hosted link's OAuth flow created (once per link; retries reuse it).
	// It is deliberately not accountId, which makes a link redeemable as an oauth-mode link.
	PendingAccountID string     `bson:"pendingAccountId,omitempty"`
	TokenHash        string     `bson:"tokenHash"`
	ExpiresAt        time.Time  `bson:"expiresAt"`
	UsedAt           *time.Time `bson:"usedAt,omitempty"`
	CreatedAt        time.Time  `bson:"createdAt"`
	CreatedBy        string     `bson:"createdBy,omitempty"`
}

// HostedCreate is POST /v1/connect-links/hosted.
type HostedCreate struct {
	ReturnURL  string `json:"returnUrl,omitempty" doc:"Where the browser goes after a successful connect: https on the EmailBridge public host or a host in the organisation branding returnUrlHosts (http on localhost only outside production)." example:"https://emailbridge.example.com/accounts?connected=1"`
	AppID      string `json:"appId,omitempty" doc:"Preselect this OAuth app (oap_…) on the page; otherwise the page offers every enabled app and password login." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	TTLSeconds int    `json:"ttlSeconds,omitempty" doc:"Link lifetime in seconds, clamped to 60 – 604 800 (7 days). Default 3600." example:"86400"`
}

// LinkCreated is the one-time create response.
type LinkCreated struct {
	ID        string    `json:"id" doc:"Connect link ID (lnk_…)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm"`
	URL       string    `json:"url" doc:"Hosted page URL to send to the mailbox owner. Shown only in this response (only a hash is stored)." example:"https://emailbridge.example.com/connect/lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
	ExpiresAt time.Time `json:"expiresAt" doc:"When the link stops working." example:"2026-10-28T09:30:00Z"`
}

// BrandingPalette holds optional light/dark colour overrides (W68).
type BrandingPalette struct {
	Light map[string]string `json:"light,omitempty" bson:"light,omitempty" doc:"Light-theme colour overrides: Vuetify colour name (primary, on-primary, background, surface, error …) → #RRGGBB. Foreground/background pairs must reach WCAG AA contrast (4.5:1)." example:"{\"primary\":\"#1D4ED8\",\"on-primary\":\"#FFFFFF\",\"background\":\"#F8FAFC\",\"on-background\":\"#0F172A\"}"`
	Dark  map[string]string `json:"dark,omitempty" bson:"dark,omitempty" doc:"Dark-theme colour overrides (same keys and rules as light)." example:"{\"primary\":\"#93C5FD\",\"on-primary\":\"#0B1220\",\"background\":\"#0B1220\",\"on-background\":\"#E2E8F0\"}"`
}

// Branding is org branding for the public page.
type Branding struct {
	BrandName       string           `json:"brandName,omitempty" doc:"Name shown on the hosted page and in its title. Default EmailBridge." example:"Acme Mail"`
	LogoAssetID     string           `json:"logoAssetId,omitempty" doc:"Uploaded logo (bas_…, kind logo)." example:"bas_01m5f3vqwgczpd0cmjwqfwh974"`
	LogoDarkAssetID string           `json:"logoDarkAssetId,omitempty" doc:"Logo for the dark theme (bas_…, kind logoDark)." example:"bas_01m5z767pg7f5ja80nwjbnbqb0"`
	FaviconAssetID  string           `json:"faviconAssetId,omitempty" doc:"Favicon (bas_…, kind favicon)." example:"bas_01m57srhxbmqd4ybm793jpc5af"`
	LogoURL         string           `json:"logoUrl,omitempty" doc:"Public URL of the logo (resolved from logoAssetId, or a legacy absolute URL)." example:"https://emailbridge.example.com/v1/public/branding/assets/bas_01m5f3vqwgczpd0cmjwqfwh974"` // resolved or legacy
	LogoDarkURL     string           `json:"logoDarkUrl,omitempty" doc:"Public URL of the dark logo." example:"https://emailbridge.example.com/v1/public/branding/assets/bas_01m5z767pg7f5ja80nwjbnbqb0"`                                              // resolved
	FaviconURL      string           `json:"faviconUrl,omitempty" doc:"Public URL of the favicon." example:"https://emailbridge.example.com/v1/public/branding/assets/bas_01m57srhxbmqd4ybm793jpc5af"`                                                 // resolved
	PrimaryColor    string           `json:"primaryColor,omitempty" doc:"Primary colour (#RRGGBB); also fills palette.light.primary." example:"#1D4ED8"`
	Palette         *BrandingPalette `json:"palette,omitempty" doc:"Light / dark colour overrides."`
	HeaderHTML      string           `json:"headerHtml,omitempty" doc:"Sanitised HTML shown above the connect form (allow-listed tags; no scripts, forms or event handlers)." example:"<div class=\"brand\"><img src=\"/v1/public/branding/assets/bas_01m5f3vqwgczpd0cmjwqfwh974\" alt=\"Acme\"><p>Connect your mailbox to <strong>Acme Mail</strong></p></div>"`
	HeadHTML        string           `json:"headHtml,omitempty" doc:"Sanitised extra <head> content: <style> (no @import or expressions) and harmless <meta>." example:"<style>.brand{display:flex;gap:12px;align-items:center}</style>"`
	ReturnURLHosts  []string         `json:"returnUrlHosts,omitempty" doc:"Extra exact hosts a hosted connect link's returnUrl may point to (https only), besides the EmailBridge public host. Never shown on the public page." example:"[\"app.example.com\"]"`
}

// BrandingUpdate is PATCH /v1/connect/branding (W53 + W68).
// Null asset id fields clear the reference (JSON null). Omitted fields leave existing values.
type BrandingUpdate struct {
	BrandName       *string          `json:"brandName,omitempty" doc:"New brand name." example:"Acme Mail"`
	LogoAssetID     *string          `json:"logoAssetId,omitempty" doc:"Logo asset (bas_…, kind logo); null removes it." example:"bas_01m5f3vqwgczpd0cmjwqfwh974"`
	LogoDarkAssetID *string          `json:"logoDarkAssetId,omitempty" doc:"Dark logo asset (kind logoDark); null removes it." example:"bas_01m5z767pg7f5ja80nwjbnbqb0"`
	FaviconAssetID  *string          `json:"faviconAssetId,omitempty" doc:"Favicon asset (kind favicon); null removes it." example:"bas_01m57srhxbmqd4ybm793jpc5af"`
	PrimaryColor    *string          `json:"primaryColor,omitempty" doc:"Primary colour (#RRGGBB)." example:"#1D4ED8"`
	Palette         *BrandingPalette `json:"palette,omitempty" doc:"Colour overrides, merged into the stored palette."`
	HeaderHTML      *string          `json:"headerHtml,omitempty" doc:"HTML above the form (≤ 8 KiB); sanitised before it is stored." example:"<div class=\"brand\"><img src=\"/v1/public/branding/assets/bas_01m5f3vqwgczpd0cmjwqfwh974\" alt=\"Acme\"><p>Connect your mailbox to <strong>Acme Mail</strong></p></div>"`
	HeadHTML        *string          `json:"headHtml,omitempty" doc:"Extra <head> content (≤ 4 KiB); sanitised before it is stored." example:"<style>.brand{display:flex;gap:12px;align-items:center}</style>"`
	ReturnURLHosts  *[]string        `json:"returnUrlHosts,omitempty" doc:"Replaces the returnUrl host allow-list: exact host names (no scheme, port, path or wildcard), at most 20; an empty list clears it." example:"[\"app.example.com\"]"`
}

// BrandingAsset is one uploaded branding image.
type BrandingAsset struct {
	ID             string    `json:"id" bson:"_id" doc:"Branding asset ID (bas_…)." example:"bas_01m5f3vqwgczpd0cmjwqfwh974"`
	OrganizationID string    `json:"-" bson:"organizationId"`
	Kind           AssetKind `json:"kind" bson:"kind" doc:"logo, logoDark, favicon or image." example:"logo"`
	ContentType    string    `json:"contentType" bson:"contentType" doc:"Detected image type." example:"image/png"`
	Width          int       `json:"width,omitempty" bson:"width,omitempty" doc:"Width in pixels." example:"480"`
	Height         int       `json:"height,omitempty" bson:"height,omitempty" doc:"Height in pixels." example:"120"`
	Size           int64     `json:"size" bson:"size" doc:"Size in bytes." example:"18342"`
	ObjectKey      string    `json:"-" bson:"objectKey"`
	ETag           string    `json:"-" bson:"etag"`
	CreatedAt      time.Time `json:"createdAt" bson:"createdAt" doc:"Upload time." example:"2026-10-26T15:12:40Z"`
	CreatedBy      string    `json:"-" bson:"createdBy,omitempty"`
	URL            string    `json:"url,omitempty" bson:"-" doc:"Public URL of the image (no authentication needed)." example:"https://emailbridge.example.com/v1/public/branding/assets/bas_01m5f3vqwgczpd0cmjwqfwh974"`
}

// BrandingAssetList is GET /v1/connect/branding/assets.
type BrandingAssetList struct {
	Data []BrandingAsset `json:"data" doc:"The uploaded assets (at most 50 per organisation)."`
}

// BrandingPreviewRequest is POST /v1/connect/branding/preview.
type BrandingPreviewRequest struct {
	HeaderHTML string `json:"headerHtml,omitempty" doc:"Header HTML to sanitise." example:"<div onclick=\"steal()\"><script>alert(1)</script><p>Connect to <b>Acme</b></p></div>"`
	HeadHTML   string `json:"headHtml,omitempty" doc:"Head HTML to sanitise." example:"<style>@import url(https://evil.example.net/x.css); .brand{color:#1D4ED8}</style>"`
}

// OAuthAppBrief is listed on the bootstrap response.
type OAuthAppBrief struct {
	ID       string `json:"id" doc:"OAuth app ID (oap_…)." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	Name     string `json:"name" doc:"App name, shown as a sign-in button." example:"Microsoft 365 – Acme"`
	Provider string `json:"provider" doc:"gmail, outlook, mailru or generic." example:"outlook"`
}

// Bootstrap is GET /v1/public/connect/{token}.
type Bootstrap struct {
	Branding  Branding        `json:"branding" doc:"Branding of the organisation that issued the link."`
	Mode      string          `json:"mode" doc:"hosted (create a new account) or oauth (connect an existing account)." example:"hosted"`
	OAuthApps []OAuthAppBrief `json:"oauthApps,omitempty" doc:"Enabled OAuth apps the user can sign in with."`
	CSRF      string          `json:"csrf" doc:"Form token to send with discover / password / oauth start (valid 30 minutes)." example:"1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w"`
	ExpiresAt time.Time       `json:"expiresAt" doc:"When the link expires." example:"2026-10-28T09:30:00Z"`
}

// PasswordInput is POST …/password.
type PasswordInput struct {
	Email    string             `json:"email" doc:"Mailbox address." example:"maria.rossi@example.com"`
	Password string             `json:"password" doc:"Mailbox password or app password; used for the login test and stored encrypted." example:"app-password-from-provider"`
	Name     string             `json:"name,omitempty" doc:"Account display name. Default: the email address." example:"Maria Rossi"`
	CSRF     string             `json:"csrf" doc:"Form token from GET /v1/public/connect/{token} (valid 30 minutes)." example:"1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w"`
	IMAP     *autoconfig.Server `json:"imap,omitempty" doc:"IMAP server (from discover); omitted = discovered again from the email domain."`
	SMTP     *autoconfig.Server `json:"smtp,omitempty" doc:"SMTP server (from discover); omitted = discovered again from the email domain."`
}

// Done is the success payload after password connect.
type Done struct {
	AccountID   string `json:"accountId" doc:"The created account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	RedirectURL string `json:"redirectUrl,omitempty" doc:"The link's returnUrl, when set: send the browser there." example:"https://emailbridge.example.com/accounts?connected=1"`
}

// OAuthStartInput is POST …/oauth/start.
type OAuthStartInput struct {
	Email string `json:"email" doc:"Mailbox address to connect." example:"maria.rossi@example.com"`
	AppID string `json:"appId" doc:"OAuth app (oap_…) the user chose; default: the app preselected on the link." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	CSRF  string `json:"csrf" doc:"Form token from GET /v1/public/connect/{token} (valid 30 minutes)." example:"1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w"`
}

// OAuthStartResult holds the authorize URL.
type OAuthStartResult struct {
	AuthorizeURL string `json:"authorizeUrl" doc:"Provider consent page; send the browser there." example:"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?client_id=3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13&login_hint=maria.rossi%40example.com&response_type=code&state=Zk9mR2xQ7tV4wY1bN6cH8jL3"`
}

// Identity is accounts.identity.
type Identity struct {
	Email        string    `json:"email" bson:"email" doc:"Pinned mailbox email address." example:"reception@example.com"`
	IMAPUser     string    `json:"imapUser" bson:"imapUser" doc:"Pinned IMAP login user." example:"reception@example.com"`
	OAuthSubject string    `json:"oauthSubject,omitempty" bson:"oauthSubject,omitempty" doc:"Pinned OAuth subject (OAuth accounts)." example:"110248495921238986420"`
	PinnedAt     time.Time `json:"pinnedAt" bson:"pinnedAt" doc:"When the identity was pinned." example:"2026-10-27T11:20:00Z"`
}

// RepinInput is POST /v1/accounts/{id}/identity/repin.
type RepinInput struct {
	Email        string `json:"email,omitempty" doc:"New expected email address. Default: the account email." example:"reception@example.com"`
	IMAPUser     string `json:"imapUser,omitempty" doc:"New expected IMAP login user." example:"reception@example.com"`
	OAuthSubject string `json:"oauthSubject,omitempty" doc:"New expected OAuth subject." example:"110248495921238986420"`
}
