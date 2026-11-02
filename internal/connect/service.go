package connect

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// AccountCreateFn creates a mailbox account for the hosted flow (wired to accounts.Service).
type AccountCreateFn func(ctx context.Context, org, name, email, password string, imap, smtp *autoconfig.Server) (accountID string, err error)

// OAuthStartFn starts a public OAuth authorize URL for the hosted flow. accountID is the link's account
// (oauth-mode links: connect that account, never create one); empty for a hosted link, where the wiring
// creates the account once per link. The link is marked used when the OAuth callback succeeds, and the
// browser then goes to returnURL (the link's stored, validated value).
type OAuthStartFn func(ctx context.Context, org, email, appID, linkID, accountID, returnURL string) (authorizeURL string, err error)

// HostedEnabledFn reports whether connect.hostedPage is on for the org.
type HostedEnabledFn func(ctx context.Context, org string) bool

// AVEnabledFn reports whether attachments.antivirus is on for the org (W68).
type AVEnabledFn func(ctx context.Context, org string) bool

// Service implements hosted connect + identity APIs.
type Service struct {
	Store      Store
	Assets     AssetStore
	Objects    ObjectPutter
	Scanner    *attachments.Scanner
	AVEnabled  AVEnabledFn
	Disco      *autoconfig.Service
	Audit      *audit.Writer
	Registry   *features.Registry
	PublicURL  string
	Env        string
	Create     AccountCreateFn
	OAuthStart OAuthStartFn
	Enabled    HostedEnabledFn
	clock      func() time.Time

	// CSRFKey signs the connect form's CSRF tokens. Tokens are stateless (HMAC over the link and an expiry), so
	// the bootstrap and the submit may land on different API nodes. Empty = a random per-process key (one node).
	CSRFKey []byte
	rateMu  sync.Mutex
	rate    map[string]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewService builds a Service with in-memory CSRF store.
func NewService(st Store, disco *autoconfig.Service) *Service {
	return &Service{
		Store: st, Disco: disco, clock: time.Now,
		CSRFKey: randomKey(), rate: map[string]*tokenBucket{},
		Registry: features.DefaultRegistry(), Env: "production",
		Enabled: func(context.Context, string) bool { return false },
	}
}

// CreateHosted mints a hosted connect link.
func (s *Service) CreateHosted(ctx context.Context, in HostedCreate) (*LinkCreated, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if !s.hostedOn(ctx, org) {
		return nil, errs.New(errs.CodeFeatureDisabled, "The hosted connect page is disabled for this organisation.")
	}
	if in.ReturnURL != "" {
		if err := validateReturnURL(s.PublicURL, in.ReturnURL, s.returnHosts(ctx, org), s.production()); err != nil {
			return nil, err
		}
	}
	if in.AppID != "" && !store.IsID(store.PrefixOAuthApp, in.AppID) {
		return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
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
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.link")
	}
	id := store.NewID(store.PrefixConnectLink)
	sum := sha256.Sum256([]byte(id + "." + secret))
	now := s.clock().UTC().Truncate(time.Millisecond)
	link := &Link{
		ID: id, OrganizationID: org, AppID: in.AppID, Mode: ModeHosted,
		ReturnURL: in.ReturnURL, TokenHash: hex.EncodeToString(sum[:]),
		ExpiresAt: now.Add(ttl), CreatedAt: now,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		link.CreatedBy = c.Actor()
	}
	if s.Store != nil {
		if err := s.Store.InsertLink(ctx, link); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "connect.link")
		}
	}
	url := strings.TrimRight(s.PublicURL, "/") + "/connect/" + id + "." + secret
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "connectLink.created", audit.Target{Type: "connectLink", ID: id}, audit.Changes{
			"mode": {After: ModeHosted},
		})
	}
	return &LinkCreated{ID: id, URL: url, ExpiresAt: link.ExpiresAt}, nil
}

// Bootstrap validates the public token and returns branding + CSRF.
func (s *Service) Bootstrap(ctx context.Context, token string) (*Bootstrap, error) {
	link, _, err := s.loadValid(ctx, token)
	if err != nil {
		return nil, err
	}
	if link.Mode == ModeHosted && !s.hostedOn(ctx, link.OrganizationID) {
		return nil, errs.New(errs.CodeFeatureDisabled, "The hosted connect page is disabled for this organisation.")
	}
	csrf := s.issueCSRF(link.TokenHash)
	branding := Branding{BrandName: "EmailBridge"}
	var apps []OAuthAppBrief
	if s.Store != nil {
		if org, err := s.Store.GetOrg(ctx, link.OrganizationID); err == nil {
			branding = s.brandingFrom(ctx, org)
		}
		apps, _ = s.Store.ListOAuthApps(ctx, link.OrganizationID)
	}
	mode := link.Mode
	if mode == "" {
		if link.AccountID != "" {
			mode = ModeOAuth
		} else {
			mode = ModeHosted
		}
	}
	branding.ReturnURLHosts = nil // admin configuration, not for the public page
	return &Bootstrap{
		Branding: branding, Mode: mode, OAuthApps: apps, CSRF: csrf, ExpiresAt: link.ExpiresAt,
	}, nil
}

// Discover runs autoconfig for the public page.
func (s *Service) Discover(ctx context.Context, token, email, csrf string) (*autoconfig.Result, error) {
	link, _, err := s.loadValid(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := s.checkCSRF(link.TokenHash, csrf); err != nil {
		return nil, err
	}
	if err := s.takePublicRate("discover:"+link.ID, 20); err != nil {
		return nil, err
	}
	if s.Disco == nil {
		return nil, errs.New(errs.CodeAutoconfigFailed, "Autoconfig is not configured.")
	}
	return s.Disco.Discover(ctx, email)
}

// PasswordConnect verifies and creates an account bound to the link's org.
func (s *Service) PasswordConnect(ctx context.Context, token string, in PasswordInput) (*Done, error) {
	link, idSecret, err := s.loadValid(ctx, token)
	if err != nil {
		return nil, err
	}
	_ = idSecret
	if err := s.checkCSRF(link.TokenHash, in.CSRF); err != nil {
		return nil, err
	}
	if !s.hostedOn(ctx, link.OrganizationID) {
		return nil, errs.New(errs.CodeFeatureDisabled, "The hosted connect page is disabled for this organisation.")
	}
	if err := s.takePublicRate("password:"+link.ID, 10); err != nil {
		return nil, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Password) == "" {
		return nil, errs.New(errs.CodeValidationFailed, "password is required.")
	}
	imap, smtp := in.IMAP, in.SMTP
	if imap == nil || smtp == nil {
		if s.Disco != nil {
			r, derr := s.Disco.Discover(ctx, email)
			if derr == nil && r != nil {
				if imap == nil {
					imap = r.IMAP
				}
				if smtp == nil {
					smtp = r.SMTP
				}
			}
		}
	}
	if imap == nil || smtp == nil || imap.Host == "" || smtp.Host == "" {
		return nil, errs.New(errs.CodeValidationFailed, "IMAP and SMTP settings are required (run discover first).")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = email
	}
	if s.Create == nil {
		return nil, errs.New(errs.CodeInternalError, "Account create is not configured.")
	}
	accountID, err := s.Create(ctx, link.OrganizationID, name, email, in.Password, imap, smtp)
	if err != nil {
		return nil, err
	}
	if s.Store != nil {
		_ = s.Store.MarkLinkUsed(ctx, link.ID, s.clock().UTC().Truncate(time.Millisecond))
		if a, err := s.Store.GetAccount(ctx, link.OrganizationID, accountID); err == nil {
			pin := Pin(Observed{Email: email, IMAPUser: expandUser(imap.User, email)}, s.clock())
			_ = s.Store.SetIdentity(ctx, link.OrganizationID, accountID, pin, a.Version)
		}
	}
	done := &Done{AccountID: accountID}
	if link.ReturnURL != "" {
		done.RedirectURL = link.ReturnURL
	}
	return done, nil
}

// OAuthStartPublic starts OAuth from the hosted page.
func (s *Service) OAuthStartPublic(ctx context.Context, token string, in OAuthStartInput) (*OAuthStartResult, error) {
	link, _, err := s.loadValid(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := s.checkCSRF(link.TokenHash, in.CSRF); err != nil {
		return nil, err
	}
	if link.Mode == ModeHosted && !s.hostedOn(ctx, link.OrganizationID) {
		return nil, errs.New(errs.CodeFeatureDisabled, "The hosted connect page is disabled for this organisation.")
	}
	if err := s.takePublicRate("oauth:"+link.ID, 10); err != nil {
		return nil, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	appID := in.AppID
	if appID == "" || (link.AccountID != "" && link.AppID != "") {
		// An oauth-mode link is bound to its account and app; the page cannot switch either.
		appID = link.AppID
	}
	if appID == "" || !store.IsID(store.PrefixOAuthApp, appID) {
		return nil, errs.New(errs.CodeValidationFailed, "appId is required.")
	}
	if s.OAuthStart == nil {
		return nil, errs.New(errs.CodeInternalError, "OAuth start is not configured.")
	}
	accountID := ""
	if link.Mode != ModeHosted {
		accountID = link.AccountID
	}
	u, err := s.OAuthStart(ctx, link.OrganizationID, email, appID, link.ID, accountID, link.ReturnURL)
	if err != nil {
		return nil, err
	}
	return &OAuthStartResult{AuthorizeURL: u}, nil
}

// Repin replaces the account identity pin.
func (s *Service) Repin(ctx context.Context, accountID string, in RepinInput) (*Identity, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if s.Store == nil {
		return nil, errs.New(errs.CodeInternalError, "store not configured")
	}
	a, err := s.Store.GetAccount(ctx, org, accountID)
	if err != nil {
		return nil, err
	}
	email := in.Email
	if email == "" {
		email = a.Email
	}
	pin := Pin(Observed{Email: email, IMAPUser: in.IMAPUser, OAuthSubject: in.OAuthSubject}, s.clock())
	if err := s.Store.SetIdentity(ctx, org, accountID, pin, a.Version); err != nil {
		return nil, err
	}
	return &pin, nil
}

func (s *Service) hostedOn(ctx context.Context, org string) bool {
	if s.Enabled != nil {
		return s.Enabled(ctx, org)
	}
	reg := s.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	sw, ok := reg.Get(switchHosted)
	if !ok {
		return false
	}
	v, _ := features.Resolve(sw, s.Env, features.Values{})
	return v.Bool != nil && *v.Bool
}

func (s *Service) loadValid(ctx context.Context, token string) (*Link, string, error) {
	id, secret, ok := splitConnectToken(token)
	if !ok {
		return nil, "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	if s.Store == nil {
		return nil, "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	link, err := s.Store.GetLink(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if link.UsedAt != nil || link.ExpiresAt.Before(s.clock()) {
		return nil, "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	sum := sha256.Sum256([]byte(id + "." + secret))
	if !subtleEqual(hex.EncodeToString(sum[:]), link.TokenHash) {
		return nil, "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	return link, id + "." + secret, nil
}

// issueCSRF returns `<expiry unix>.<HMAC(key, tokenHash|expiry)>`: bound to the link, valid on every node
// that shares the key, until csrfTTL.
func (s *Service) issueCSRF(tokenHash string) string {
	exp := strconv.FormatInt(s.clock().Add(csrfTTL).Unix(), 10)
	return exp + "." + s.csrfMAC(tokenHash, exp)
}

func (s *Service) checkCSRF(tokenHash, got string) error {
	bad := errs.New(errs.CodeConnectCSRFInvalid, "The connect form token is invalid or expired.")
	exp, mac, ok := strings.Cut(got, ".")
	if !ok {
		return bad
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || s.clock().After(time.Unix(unix, 0)) || !subtleEqual(s.csrfMAC(tokenHash, exp), mac) {
		return bad
	}
	return nil
}

func (s *Service) csrfMAC(tokenHash, exp string) string {
	h := hmac.New(sha256.New, s.CSRFKey)
	h.Write([]byte("connect-csrf|" + tokenHash + "|" + exp))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func randomKey() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

func (s *Service) takePublicRate(key string, perHour float64) error {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := s.clock()
	b := s.rate[key]
	if b == nil {
		b = &tokenBucket{tokens: perHour, last: now}
		s.rate[key] = b
	}
	elapsed := now.Sub(b.last).Hours()
	b.tokens += elapsed * perHour
	if b.tokens > perHour {
		b.tokens = perHour
	}
	b.last = now
	if b.tokens < 1 {
		return errs.New(errs.CodeRateLimited, "Too many connect attempts; try again later.")
	}
	b.tokens--
	return nil
}

func (s *Service) brandingFrom(ctx context.Context, o *orgDoc) Branding {
	b := Branding{BrandName: o.Name}
	if o.Branding == nil {
		return b
	}
	if v, ok := o.Branding["brandName"].(string); ok && v != "" {
		b.BrandName = v
	}
	if v, ok := o.Branding["logoAssetId"].(string); ok {
		b.LogoAssetID = v
	}
	if v, ok := o.Branding["logoDarkAssetId"].(string); ok {
		b.LogoDarkAssetID = v
	}
	if v, ok := o.Branding["faviconAssetId"].(string); ok {
		b.FaviconAssetID = v
	}
	if b.LogoAssetID != "" {
		b.LogoURL = s.resolveAssetURLCtx(ctx, o.ID, b.LogoAssetID)
	} else if v, ok := o.Branding["logoRef"].(string); ok {
		b.LogoURL = v // legacy external URL
	}
	if b.LogoDarkAssetID != "" {
		b.LogoDarkURL = s.resolveAssetURLCtx(ctx, o.ID, b.LogoDarkAssetID)
	}
	if b.FaviconAssetID != "" {
		b.FaviconURL = s.resolveAssetURLCtx(ctx, o.ID, b.FaviconAssetID)
	}
	b.Palette = paletteFromBrand(o.Branding)
	if b.Palette != nil && b.Palette.Light != nil {
		if p := b.Palette.Light["primary"]; p != "" {
			b.PrimaryColor = p
		}
	}
	if b.PrimaryColor == "" {
		if v, ok := o.Branding["primaryColor"].(string); ok {
			b.PrimaryColor = v
		}
	}
	if v, ok := o.Branding["headerHtml"].(string); ok && v != "" {
		b.HeaderHTML = SanitizeHeaderHTML(v)
	}
	if v, ok := o.Branding["headHtml"].(string); ok && v != "" {
		b.HeadHTML = SanitizeHeadHTML(v)
	}
	b.ReturnURLHosts = stringList(o.Branding["returnUrlHosts"])
	return b
}

func (s *Service) resolveAssetURLCtx(ctx context.Context, org, assetID string) string {
	if assetID == "" || s.Assets == nil {
		return ""
	}
	a, err := s.Assets.GetAsset(ctx, assetID)
	if err != nil || a.OrganizationID != org {
		return ""
	}
	return s.publicAssetURL(assetID)
}

// GetBranding returns the caller's organisation branding (sanitised HTML).
func (s *Service) GetBranding(ctx context.Context) (*Branding, int64, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, 0, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, 0, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if s.Store == nil {
		return nil, 0, errs.New(errs.CodeInternalError, "store not configured")
	}
	o, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		return nil, 0, err
	}
	b := s.brandingFrom(ctx, o)
	return &b, o.Version, nil
}

// UpdateBranding patches organisation branding; HTML is sanitised before store.
func (s *Service) UpdateBranding(ctx context.Context, in BrandingUpdate, ifMatch string) (*Branding, int64, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		if err2 := tenancy.Require(ctx, tenancy.PermAccountsWrite); err2 != nil {
			return nil, 0, err
		}
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, 0, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if s.Store == nil {
		return nil, 0, errs.New(errs.CodeInternalError, "store not configured")
	}
	o, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		return nil, 0, err
	}
	if ifMatch != "" {
		want := strings.Trim(ifMatch, `"`)
		if want != "" && want != strconv.FormatInt(o.Version, 10) {
			return nil, 0, errs.New(errs.CodeVersionMismatch, "If-Match does not match the current version.")
		}
	}
	brand := map[string]any{}
	if o.Branding != nil {
		for k, v := range o.Branding {
			brand[k] = v
		}
	}
	if in.BrandName != nil {
		brand["brandName"] = strings.TrimSpace(*in.BrandName)
	}
	if in.LogoAssetID != nil {
		id := strings.TrimSpace(*in.LogoAssetID)
		if id == "" {
			delete(brand, "logoAssetId")
		} else {
			if err := s.requireAsset(ctx, org, id, AssetKindLogo); err != nil {
				return nil, 0, err
			}
			brand["logoAssetId"] = id
			delete(brand, "logoRef") // asset replaces legacy URL
		}
	}
	if in.LogoDarkAssetID != nil {
		id := strings.TrimSpace(*in.LogoDarkAssetID)
		if id == "" {
			delete(brand, "logoDarkAssetId")
		} else {
			if err := s.requireAsset(ctx, org, id, AssetKindLogoDark); err != nil {
				return nil, 0, err
			}
			brand["logoDarkAssetId"] = id
		}
	}
	if in.FaviconAssetID != nil {
		id := strings.TrimSpace(*in.FaviconAssetID)
		if id == "" {
			delete(brand, "faviconAssetId")
		} else {
			if err := s.requireAsset(ctx, org, id, AssetKindFavicon); err != nil {
				return nil, 0, err
			}
			brand["faviconAssetId"] = id
		}
	}
	if in.PrimaryColor != nil {
		col := strings.TrimSpace(*in.PrimaryColor)
		brand["primaryColor"] = col
		pal := mergePalette(asAnyMap(brand["palette"]), &BrandingPalette{
			Light: map[string]string{"primary": col},
		})
		if err := validatePalette(paletteFromBrand(map[string]any{"palette": pal})); err != nil {
			return nil, 0, err
		}
		brand["palette"] = pal
	}
	if in.Palette != nil {
		if err := validatePalette(in.Palette); err != nil {
			return nil, 0, err
		}
		pal := mergePalette(asAnyMap(brand["palette"]), in.Palette)
		if err := validatePalette(paletteFromBrand(map[string]any{"palette": pal})); err != nil {
			return nil, 0, err
		}
		brand["palette"] = pal
		if in.Palette.Light != nil {
			if p, ok := in.Palette.Light["primary"]; ok && p != "" {
				brand["primaryColor"] = strings.ToUpper(strings.TrimSpace(p))
			}
		}
	}
	if in.HeaderHTML != nil {
		brand["headerHtml"] = SanitizeHeaderHTML(*in.HeaderHTML)
	}
	if in.HeadHTML != nil {
		brand["headHtml"] = SanitizeHeadHTML(*in.HeadHTML)
	}
	if in.ReturnURLHosts != nil {
		hosts, err := normalizeReturnHosts(*in.ReturnURLHosts)
		if err != nil {
			return nil, 0, err
		}
		if len(hosts) == 0 {
			delete(brand, "returnUrlHosts")
		} else {
			brand["returnUrlHosts"] = hosts
		}
	}
	updated, err := s.Store.SetOrgBranding(ctx, org, brand, o.Version)
	if err != nil {
		return nil, 0, err
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "organization.branding.updated", audit.Target{Type: "organization", ID: org}, audit.Changes{
			"branding": {After: brand},
		})
	}
	b := s.brandingFrom(ctx, updated)
	return &b, updated.Version, nil
}

func asAnyMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	default:
		return map[string]any{}
	}
}

// PreviewBrandingHTML sanitises header/head HTML without persisting.
func (s *Service) PreviewBrandingHTML(ctx context.Context, in BrandingPreviewRequest) (*BrandingPreview, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	out := PreviewBranding(in.HeaderHTML, in.HeadHTML)
	return &out, nil
}

// validateReturnURL guards the post-connect redirect against open redirects. The URL must be absolute,
// without credentials, https, and its host must be exactly the EmailBridge public host or one of the
// organisation's branding.returnUrlHosts. http (and any port) on localhost / 127.0.0.1 / ::1 is accepted
// only outside production.
func validateReturnURL(public, ret string, extraHosts []string, production bool) error {
	u, err := url.Parse(strings.TrimSpace(ret))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errs.New(errs.CodeValidationFailed, "returnUrl is not a valid URL.")
	}
	if u.User != nil {
		return errs.New(errs.CodeValidationFailed, "returnUrl must not contain credentials.")
	}
	host := strings.ToLower(u.Hostname())
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if local && !production && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" {
		return errs.New(errs.CodeValidationFailed, "returnUrl must be https (http only on localhost outside production).")
	}
	allowed := map[string]bool{}
	if pub, err := url.Parse(public); err == nil && pub.Hostname() != "" {
		allowed[strings.ToLower(pub.Hostname())] = true
	}
	for _, h := range extraHosts {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	if !allowed[host] {
		return errs.New(errs.CodeValidationFailed, "returnUrl host is not allowed.").WithSafe("host", host)
	}
	return nil
}

const maxReturnHosts = 20

// returnHostRE is one DNS host name: labels of letters, digits and hyphens (no leading/trailing hyphen),
// at least one dot. No wildcards, ports, schemes or paths: hosts are matched exactly.
var returnHostRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// normalizeReturnHosts validates and de-duplicates a returnUrlHosts list (lower-cased).
func normalizeReturnHosts(in []string) ([]string, error) {
	if len(in) > maxReturnHosts {
		return nil, errs.New(errs.CodeValidationFailed, "returnUrlHosts has too many entries.").WithSafe("max", maxReturnHosts)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if len(h) > 253 || !returnHostRE.MatchString(h) {
			return nil, errs.New(errs.CodeValidationFailed,
				"returnUrlHosts entries are exact host names (no scheme, port, path or wildcard).").WithSafe("host", raw)
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out, nil
}

// returnHosts loads the organisation's branding.returnUrlHosts (nil when none or unreadable).
func (s *Service) returnHosts(ctx context.Context, org string) []string {
	if s.Store == nil {
		return nil
	}
	o, err := s.Store.GetOrg(ctx, org)
	if err != nil || o == nil || o.Branding == nil {
		return nil
	}
	return stringList(o.Branding["returnUrlHosts"])
}

// production reports whether the process runs in production (unknown counts as production).
func (s *Service) production() bool {
	env := strings.ToLower(strings.TrimSpace(s.Env))
	return env == "" || env == "production" || env == "prod"
}

// stringList reads a stored string array (BSON arrays decode as bson.A / []any).
func stringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		out = append(out, t...)
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case bson.A:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func normalizeEmail(raw string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || a == nil || a.Address == "" {
		return "", errs.New(errs.CodeValidationFailed, "A valid email address is required.")
	}
	return strings.ToLower(a.Address), nil
}

func expandUser(tmpl, email string) string {
	switch tmpl {
	case "", "%EMAIL%", "%EMAILADDRESS%":
		return email
	case "%EMAILLOCALPART%":
		at := strings.IndexByte(email, '@')
		if at > 0 {
			return email[:at]
		}
		return email
	default:
		return tmpl
	}
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

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
