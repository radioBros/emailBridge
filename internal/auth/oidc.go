package auth

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/store"
)

// OIDCProvidersCollection holds instance-wide OIDC SSO providers (P3.4b).
const OIDCProvidersCollection = "oidcProviders"

const (
	oidcStateTTL     = 10 * time.Minute
	oidcStateKeyPref = "eb:oidc:state:"
	oidcCookieName   = "eb_oidc" // unused; state is server-side (SameSite=Strict)
	MethodOIDC       = "oidc"
)

// InviteChecker reports whether email has a valid pending invitation (any org). Wired by tenancy.
type InviteChecker func(ctx context.Context, email string) (bool, error)

// ProvisionHook runs after OIDC sign-in resolves a Limen user (create or re-login).
// providerID and groupRoles come from the OIDC provider so tenancy needs no auth import.
type ProvisionHook func(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error

// OIDCProvider is the admin view of an OIDC IdP (secret never exposed).
type OIDCProvider struct {
	ID                  string                 `json:"id" bson:"_id" doc:"Provider ID (oidc_…)." example:"oidc_01m5d68e607knapbtf5smmk5re"`
	IssuerURL           string                 `json:"issuerUrl" bson:"issuerUrl" doc:"OpenID Provider issuer URL (discovery at {issuer}/.well-known/openid-configuration)." example:"https://auth.example.com/application/o/emailbridge"`
	ClientID            string                 `json:"clientId" bson:"clientId" doc:"OAuth client ID registered at the provider." example:"emailbridge-admin"`
	ClientSecret        crypto.EncryptedString `json:"-" bson:"clientSecret"`
	Scopes              []string               `json:"scopes" bson:"scopes" doc:"Scopes requested at sign-in (default openid, email, profile)." example:"[\"openid\",\"email\",\"profile\",\"groups\"]"`
	ButtonLabel         string                 `json:"buttonLabel" bson:"buttonLabel" doc:"Label of the sign-in button on the login page." example:"Sign in with Authentik"`
	AllowedEmailDomains []string               `json:"allowedEmailDomains,omitempty" bson:"allowedEmailDomains,omitempty" doc:"Only verified emails in these domains may sign in (empty = any domain)." example:"[\"example.com\"]"`
	GroupClaim          string                 `json:"groupClaim,omitempty" bson:"groupClaim,omitempty" doc:"ID-token claim holding the user's groups, used with groupRoles." example:"groups"`
	GroupRoles          map[string]string      `json:"groupRoles,omitempty" bson:"groupRoles,omitempty" doc:"Group name → org_<id>:<role> granted on sign-in (role owner, admin, operator or viewer)." example:"{\"emailbridge-admins\":\"org_01m5caqpk0kast63yc13wpy2d9:admin\",\"support\":\"org_01m5caqpk0kast63yc13wpy2d9:operator\"}"`
	AutoProvision       bool                   `json:"autoProvision" bson:"autoProvision" doc:"Create the user on first sign-in; otherwise only existing users and emails with a pending invitation may sign in." example:"true"`
	Enabled             bool                   `json:"enabled" bson:"enabled" doc:"Whether the provider is offered on the login page." example:"true"`
	CreatedAt           time.Time              `json:"createdAt" bson:"createdAt" doc:"Creation time." example:"2026-10-22T09:12:00Z"`
	UpdatedAt           time.Time              `json:"updatedAt" bson:"updatedAt" doc:"Last change." example:"2026-10-25T16:40:00Z"`
}

// OIDCPublicProvider is what the login page needs.
type OIDCPublicProvider struct {
	ID          string `json:"id" doc:"Provider ID; start the sign-in at /v1/auth/oidc/{id}/start." example:"oidc_01m5d68e607knapbtf5smmk5re"`
	ButtonLabel string `json:"buttonLabel" doc:"Button label." example:"Sign in with Authentik"`
}

type oidcState struct {
	ProviderID  string `json:"providerId"`
	Verifier    string `json:"verifier"`
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirectUri"`
}

// Migration creates oidcProviders indexes (P3.4b, migration 23).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "oidcProviders indexes", Up: EnsureOIDCIndexes}
}

// EnsureOIDCIndexes creates indexes for oidcProviders.
func EnsureOIDCIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, OIDCProvidersCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "issuerUrl", Value: 1}, {Key: "clientId", Value: 1}}, Options: options.Index().SetUnique(true)},
		mongo.IndexModel{Keys: bson.D{{Key: "enabled", Value: 1}}},
	)
}

func defaultOIDCScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"openid", "email", "profile"}
	}
	out := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if !seen["openid"] {
		out = append([]string{"openid"}, out...)
	}
	return out
}

func emailDomain(email string) string {
	_, dom, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok {
		return ""
	}
	return dom
}

func domainAllowed(email string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	dom := emailDomain(email)
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), dom) {
			return true
		}
	}
	return false
}
