package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// tenantIDRe accepts a Microsoft tenant GUID or the well-known aliases.
var tenantIDRe = regexp.MustCompile(`(?i)^(common|organizations|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

// StateStore persists short-lived OAuth state (NATS cache in production).
type StateStore interface {
	CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error
	CacheGet(ctx context.Context, key string) (bus.CacheEntry, error)
	CacheDelete(ctx context.Context, key string) error
}

// LeaseStore claims per-account refresh leases.
type LeaseStore interface {
	Claim(ctx context.Context, key string, value []byte, ttl time.Duration) (uint64, error)
	Release(ctx context.Context, key string, epoch uint64) error
}

// Service is OAuth app CRUD, flow, refresh and connect links.
type Service struct {
	Repo        *Repo
	Cipher      *crypto.Cipher
	Egress      *egress.Dialer
	Audit       *audit.Writer
	State       StateStore
	Leases      LeaseStore
	NodeID      string
	CallbackURL string // EB_CALLBACK_URL
	PublicURL   string // EB_PUBLIC_URL
	HTTP        *http.Client
	Log         *slog.Logger
	// GmailAPIRoot / GraphAPIRoot override production API hosts (tests / simulators).
	GmailAPIRoot string
	GraphAPIRoot string
	now          func() time.Time
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

func (s *Service) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	if s.Egress != nil {
		return s.Egress.HTTPClient(30 * time.Second)
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Create inserts an OAuth app.
func (s *Service) Create(ctx context.Context, in CreateInput) (*App, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	n, err := s.Repo.countApps(ctx)
	if err != nil {
		return nil, err
	}
	if n >= maxApps {
		return nil, errs.New(errs.CodeValidationFailed, "Organisation OAuth app limit reached (50).")
	}
	now := s.clock().Truncate(time.Millisecond)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	scopes := in.Scopes
	if len(scopes) == 0 {
		if p, ok := presets[in.Provider]; ok {
			scopes = append([]string(nil), p.Scopes...)
		}
	}
	a := &App{
		ID: store.NewID(store.PrefixOAuthApp), OrganizationID: org, Name: strings.TrimSpace(in.Name),
		Provider: in.Provider, ClientID: strings.TrimSpace(in.ClientID),
		TenantID:     strings.TrimSpace(in.TenantID),
		AuthorizeURL: strings.TrimSpace(in.AuthorizeURL), TokenURL: strings.TrimSpace(in.TokenURL),
		RevokeURL: strings.TrimSpace(in.RevokeURL), Scopes: scopes, Enabled: enabled,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if in.AppOnly != nil {
		a.AppOnly = *in.AppOnly
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		a.CreatedBy = c.UserID
		if c.Kind == tenancy.CallerAPIKey {
			a.CreatedBy = c.APIKeyID
		}
	}
	if err := validateTenantID(a.TenantID); err != nil {
		return nil, err
	}
	sec, err := s.Cipher.EncryptString(ctx, org, in.ClientSecret, crypto.AAD{
		Collection: CollectionApps, Field: "clientSecret", DocID: a.ID,
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.encrypt")
	}
	a.ClientSecret = sec
	if sk := strings.TrimSpace(in.ServiceKey); sk != "" {
		if err := validateServiceKeyJSON(sk); err != nil {
			return nil, err
		}
		enc, err := s.Cipher.EncryptString(ctx, org, sk, crypto.AAD{
			Collection: CollectionApps, Field: "serviceKey", DocID: a.ID,
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.encrypt.serviceKey")
		}
		a.ServiceKey = enc
	}
	if err := s.Repo.insertApp(ctx, a); err != nil {
		return nil, err
	}
	s.audit(ctx, "oauthApp.created", a.ID, audit.Changes{"name": {After: a.Name}, "provider": {After: a.Provider}})
	return a, nil
}

// Get returns one app.
func (s *Service) Get(ctx context.Context, id string) (*App, error) {
	if err := tenancy.RequireRead(ctx, "oauth"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixOAuthApp, id) {
		return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
	}
	return s.Repo.getApp(ctx, id)
}

// List pages apps.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "oauth"); err != nil {
		return nil, err
	}
	return s.Repo.listApps(ctx, f)
}

// Patch updates an app.
func (s *Service) Patch(ctx context.Context, id, ifMatch string, in PatchInput) (*App, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	a, err := s.Repo.getApp(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, a.Version); err != nil {
		return nil, err
	}
	old := *a
	if in.Name != nil {
		a.Name = strings.TrimSpace(*in.Name)
	}
	if in.ClientID != nil {
		a.ClientID = strings.TrimSpace(*in.ClientID)
	}
	if in.AuthorizeURL != nil {
		a.AuthorizeURL = strings.TrimSpace(*in.AuthorizeURL)
	}
	if in.TokenURL != nil {
		a.TokenURL = strings.TrimSpace(*in.TokenURL)
	}
	if in.RevokeURL != nil {
		a.RevokeURL = strings.TrimSpace(*in.RevokeURL)
	}
	if in.Scopes != nil {
		a.Scopes = in.Scopes
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		sec, err := s.Cipher.EncryptString(ctx, a.OrganizationID, *in.ClientSecret, crypto.AAD{
			Collection: CollectionApps, Field: "clientSecret", DocID: a.ID,
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.encrypt")
		}
		a.ClientSecret = sec
	}
	if in.ServiceKey != nil {
		sk := strings.TrimSpace(*in.ServiceKey)
		if sk == "" {
			a.ServiceKey = crypto.EncryptedString{} // clear (W43 revocation)
		} else {
			if err := validateServiceKeyJSON(sk); err != nil {
				return nil, err
			}
			enc, err := s.Cipher.EncryptString(ctx, a.OrganizationID, sk, crypto.AAD{
				Collection: CollectionApps, Field: "serviceKey", DocID: a.ID,
			})
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "oauth.encrypt.serviceKey")
			}
			a.ServiceKey = enc
		}
	}
	if in.TenantID != nil {
		a.TenantID = strings.TrimSpace(*in.TenantID)
	}
	if in.AppOnly != nil {
		a.AppOnly = *in.AppOnly
	}
	if err := validateTenantID(a.TenantID); err != nil {
		return nil, err
	}
	if err := validateApp(a); err != nil {
		return nil, err
	}
	a.UpdatedAt = s.clock().Truncate(time.Millisecond)
	a.Version++
	if err := s.Repo.replaceApp(ctx, a, old.Version); err != nil {
		return nil, err
	}
	s.audit(ctx, "oauthApp.updated", a.ID, audit.Changes{"name": {Before: old.Name, After: a.Name}})
	return a, nil
}

// Delete removes an app.
func (s *Service) Delete(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return err
	}
	a, err := s.Repo.getApp(ctx, id)
	if err != nil {
		return err
	}
	if err := checkIfMatch(ifMatch, a.Version); err != nil {
		return err
	}
	if err := s.refuseDeleteIfInUse(ctx, a.OrganizationID, id); err != nil {
		return err
	}
	if err := s.Repo.deleteApp(ctx, a); err != nil {
		return err
	}
	s.audit(ctx, "oauthApp.deleted", id, audit.Changes{"name": {Before: a.Name}})
	return nil
}

// refuseDeleteIfInUse blocks deleting an OAuth app still referenced by accounts.oauth.appId.
func (s *Service) refuseDeleteIfInUse(ctx context.Context, orgID, appID string) error {
	if s.Repo == nil || s.Repo.DB == nil {
		return nil
	}
	n, err := s.Repo.DB.Collection("accounts").CountDocuments(ctx, bson.M{
		"organizationId": orgID, "oauth.appId": appID,
	})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "oauth.app.inuse")
	}
	if n > 0 {
		return errs.New(errs.CodeOauthAppInUse,
			"Accounts still reference this OAuth app; disconnect them first.").
			WithSafe("count", n)
	}
	return nil
}

func (s *Service) decryptSecret(ctx context.Context, a *App) (string, error) {
	return s.Cipher.DecryptString(ctx, a.ClientSecret, crypto.AAD{
		Collection: CollectionApps, Field: "clientSecret", DocID: a.ID,
	})
}

// DecryptServiceKey returns the plaintext Google service-account JSON (never log it).
func (s *Service) DecryptServiceKey(ctx context.Context, a *App) (string, error) {
	if a == nil || a.ServiceKey.IsZero() {
		return "", errs.New(errs.CodeOauthNotConnected, "Service account key is not configured on this OAuth app.")
	}
	return s.Cipher.DecryptString(ctx, a.ServiceKey, crypto.AAD{
		Collection: CollectionApps, Field: "serviceKey", DocID: a.ID,
	})
}

// ServiceAccountJSON loads an app and decrypts its service key (domain-wide delegation).
func (s *Service) ServiceAccountJSON(ctx context.Context, appID string) ([]byte, error) {
	a, err := s.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	plain, err := s.DecryptServiceKey(ctx, a)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}

// ClientCredentialsMaterial is the decrypted app-only / token-endpoint material for Graph (W38).
type ClientCredentialsMaterial struct {
	ClientID string
	Secret   string
	TenantID string // empty stored → "common"
	AppOnly  bool
}

// TokenURL is the Microsoft identity platform token endpoint for this tenant.
func (m ClientCredentialsMaterial) TokenURL() string {
	return "https://login.microsoftonline.com/" + m.TenantID + "/oauth2/v2.0/token"
}

// ClientCredentialsMaterial decrypts the client secret and returns tenant / app-only flags.
func (s *Service) ClientCredentialsMaterial(ctx context.Context, a *App) (ClientCredentialsMaterial, error) {
	if a == nil {
		return ClientCredentialsMaterial{}, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
	}
	sec, err := s.decryptSecret(ctx, a)
	if err != nil {
		return ClientCredentialsMaterial{}, errs.Wrap(err, errs.CodeInternalError, "oauth.decrypt")
	}
	tenant := strings.TrimSpace(a.TenantID)
	if tenant == "" {
		tenant = "common"
	}
	return ClientCredentialsMaterial{
		ClientID: a.ClientID, Secret: sec, TenantID: tenant, AppOnly: a.AppOnly,
	}, nil
}

func (s *Service) endpoints(a *App) (authorize, token, revoke string) {
	return resolveEndpoints(a)
}

func resolveEndpoints(a *App) (authorize, token, revoke string) {
	authorize, token, revoke = a.AuthorizeURL, a.TokenURL, a.RevokeURL
	if p, ok := presets[a.Provider]; ok {
		if authorize == "" {
			authorize = p.AuthorizeURL
		}
		if token == "" {
			token = p.TokenURL
		}
		if revoke == "" {
			revoke = p.RevokeURL
		}
	}
	if a.Provider == ProviderOutlook {
		tenant := strings.TrimSpace(a.TenantID)
		if tenant == "" {
			tenant = "common"
		}
		authorize = strings.Replace(authorize, "/common/", "/"+tenant+"/", 1)
		token = strings.Replace(token, "/common/", "/"+tenant+"/", 1)
	}
	return
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "oauthApp", ID: id}, changes)
}

func (s *Service) auditAccount(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "account", ID: id}, changes)
}

func validateCreate(in CreateInput) error {
	a := &App{
		Name: in.Name, Provider: in.Provider, ClientID: in.ClientID,
		TenantID: in.TenantID, AuthorizeURL: in.AuthorizeURL, TokenURL: in.TokenURL,
		RevokeURL: in.RevokeURL, Scopes: in.Scopes,
	}
	if strings.TrimSpace(in.ClientSecret) == "" {
		return errs.New(errs.CodeValidationFailed, "clientSecret is required.")
	}
	if err := validateTenantID(strings.TrimSpace(in.TenantID)); err != nil {
		return err
	}
	if sk := strings.TrimSpace(in.ServiceKey); sk != "" {
		if err := validateServiceKeyJSON(sk); err != nil {
			return err
		}
	}
	return validateApp(a)
}

func validateApp(a *App) error {
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 200 {
		return errs.New(errs.CodeValidationFailed, "name must be 1 to 200 characters.")
	}
	switch a.Provider {
	case ProviderGmail, ProviderOutlook, ProviderMailru, ProviderGeneric:
	default:
		return errs.New(errs.CodeValidationFailed, "provider must be gmail, outlook, mailru or generic.")
	}
	if strings.TrimSpace(a.ClientID) == "" || len(a.ClientID) > 512 {
		return errs.New(errs.CodeValidationFailed, "clientId is required.")
	}
	if a.Provider == ProviderGeneric {
		if strings.TrimSpace(a.AuthorizeURL) == "" || strings.TrimSpace(a.TokenURL) == "" {
			return errs.New(errs.CodeValidationFailed, "authorizeUrl and tokenUrl are required for generic providers.")
		}
		if len(a.Scopes) == 0 {
			return errs.New(errs.CodeValidationFailed, "scopes are required for generic providers.")
		}
	}
	return validateTenantID(a.TenantID)
}

func validateTenantID(tenant string) error {
	if tenant == "" {
		return nil
	}
	if !tenantIDRe.MatchString(tenant) {
		return errs.New(errs.CodeValidationFailed, "tenantId must be a GUID, common, or organizations.")
	}
	return nil
}

// validateServiceKeyJSON checks a Google service-account key without logging its contents.
func validateServiceKeyJSON(raw string) error {
	var k struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		return errs.New(errs.CodeValidationFailed, "serviceKey must be a Google service-account JSON key.")
	}
	if k.Type != "service_account" {
		return errs.New(errs.CodeValidationFailed, "serviceKey type must be service_account.")
	}
	if strings.TrimSpace(k.ClientEmail) == "" {
		return errs.New(errs.CodeValidationFailed, "serviceKey must include client_email.")
	}
	if strings.TrimSpace(k.PrivateKey) == "" {
		return errs.New(errs.CodeValidationFailed, "serviceKey must include private_key.")
	}
	return nil
}

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch == "" {
		return nil
	}
	want := formatETag(version)
	if ifMatch != want && ifMatch != strings.Trim(want, `"`) {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

func callbackBase(callbackURL string) string {
	return strings.TrimRight(callbackURL, "/")
}
