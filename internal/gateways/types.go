// Package gateways owns organisation SMTP and API relay configurations (P5.3, W36).
package gateways

import (
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collection is smtpGateways (S1 §6).
const Collection = "smtpGateways"

const (
	TLSImplicit = "implicit"
	TLSStartTLS = "starttls"
	TLSNone     = "none"

	PolicyStandard   = "standard"
	PolicyAtMostOnce = "atMostOnce"

	KindSMTP = "smtp"
)

// Gateway is the stored smtpGateways document.
type Gateway struct {
	ID               string     `bson:"_id" json:"id"`
	OrganizationID   string     `bson:"organizationId" json:"organizationId"`
	Name             string     `bson:"name" json:"name"`
	Kind             string     `bson:"kind,omitempty" json:"kind,omitempty"` // empty → smtp
	Host             string     `bson:"host,omitempty" json:"host,omitempty"`
	Port             int        `bson:"port,omitempty" json:"port,omitempty"`
	TLS              string     `bson:"tls,omitempty" json:"tls,omitempty"`
	Auth             Auth       `bson:"auth,omitempty" json:"-"`
	API              *APIConfig `bson:"api,omitempty" json:"-"`
	APICreds         *APICreds  `bson:"apiCreds,omitempty" json:"-"`
	EHLOName         string     `bson:"ehloName,omitempty" json:"ehloName,omitempty"`
	LocalAddress     string     `bson:"localAddress,omitempty" json:"localAddress,omitempty"`
	Proxy            string     `bson:"proxy,omitempty" json:"proxy,omitempty"`
	Policy           string     `bson:"policy,omitempty" json:"policy,omitempty"`
	MaxAttempts      *int       `bson:"maxAttempts,omitempty" json:"maxAttempts,omitempty"`
	BackoffBase      string     `bson:"backoffBase,omitempty" json:"backoffBase,omitempty"`
	IgnoreCertErrors bool       `bson:"ignoreCertErrors,omitempty" json:"ignoreCertErrors,omitempty"`
	Enabled          bool       `bson:"enabled" json:"enabled"`
	DKIM             *DKIM      `bson:"dkim,omitempty" json:"-"`
	CreatedAt        time.Time  `bson:"createdAt" json:"createdAt"`
	UpdatedAt        time.Time  `bson:"updatedAt" json:"updatedAt"`
	Version          int64      `bson:"version" json:"version"`
	CreatedBy        string     `bson:"createdBy,omitempty" json:"-"`
}

// EffectiveKind returns kind or smtp when unset.
func (g *Gateway) EffectiveKind() string {
	if g == nil || g.Kind == "" {
		return KindSMTP
	}
	return g.Kind
}

// IsAPI reports whether this gateway is an HTTP API provider.
func (g *Gateway) IsAPI() bool {
	k := g.EffectiveKind()
	return k != KindSMTP
}

// Auth holds encrypted SMTP credentials.
type Auth struct {
	User string                 `bson:"user"`
	Pass crypto.EncryptedString `bson:"pass"`
}

// APIConfig is non-secret provider options.
type APIConfig struct {
	Region  string            `bson:"region,omitempty" json:"region,omitempty" doc:"Provider region (ses: AWS region; mailgun: us or eu)." example:"eu-west-1"`
	Domain  string            `bson:"domain,omitempty" json:"domain,omitempty" doc:"Sending domain (required for mailgun)." example:"mg.example.com"`
	BaseURL string            `bson:"baseUrl,omitempty" json:"baseUrl,omitempty" doc:"Override of the provider API base URL (private endpoints); must pass the egress policy." example:"https://api.eu.mailgun.net"`
	From    string            `bson:"from,omitempty" json:"from,omitempty" doc:"Default From address for API sends when the message has none." example:"noreply@example.com"`
	Extra   map[string]string `bson:"extra,omitempty" json:"extra,omitempty" doc:"Provider-specific options (name → value), e.g. a Postmark message stream or a SES configuration set." example:"{\"messageStream\":\"outbound\",\"configurationSet\":\"emailbridge-prod\"}"`
}

// APICreds holds encrypted API secrets.
type APICreds struct {
	AccessKey     crypto.EncryptedString `bson:"accessKey,omitempty"`
	SecretKey     crypto.EncryptedString `bson:"secretKey,omitempty"`
	APIKey        crypto.EncryptedString `bson:"apiKey,omitempty"`
	WebhookSecret crypto.EncryptedString `bson:"webhookSecret,omitempty"`
}

// DKIM is optional signing material (key encrypted; signing wired later).
type DKIM struct {
	Domain     string                 `bson:"domain"`
	Selector   string                 `bson:"selector"`
	PrivateKey crypto.EncryptedString `bson:"privateKey"`
}

// AuthInput is write-side SMTP auth.
type AuthInput struct {
	User string `json:"user" doc:"SMTP login user (required for SMTP gateways)." example:"relay@example.com"`
	Pass string `json:"pass,omitempty" doc:"SMTP password. Write-only: stored encrypted, never returned (responses show auth.hasPassword)." example:"relay-password-2026"`
}

// CredentialsInput is write-only API secrets.
type CredentialsInput struct {
	AccessKey     string `json:"accessKey,omitempty" doc:"ses: AWS access key ID. Write-only." example:"AKIAIOSFODNN7EXAMPLE"`
	SecretKey     string `json:"secretKey,omitempty" doc:"ses: AWS secret access key. Write-only." example:"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`
	APIKey        string `json:"apiKey,omitempty" doc:"postmark (server token), mailgun, sendgrid, brevo: API key. Write-only." example:"SG.r8Kq2mV7tZ1xR9pW3bN6cL.0dH4jF5gY2sA8vB1nM7kQ3wE6rT9yU2iO5pL8zX1c"`
	WebhookSecret string `json:"webhookSecret,omitempty" doc:"Secret that authenticates the provider delivery webhook (mailgun signing key, sendgrid public key, postmark / brevo token). Write-only." example:"whsec_4f8Kq2mV7tZ1xR9pW3bN6cL0"`
}

// CreateInput is POST /v1/smtp-gateways.
type CreateInput struct {
	Name             string            `json:"name" doc:"Display name, unique in the organisation (1–200 characters)." example:"Office relay"`
	Kind             string            `json:"kind,omitempty" doc:"smtp (default), ses, postmark, mailgun, sendgrid or brevo. Immutable after create." example:"smtp"`
	Host             string            `json:"host,omitempty" doc:"SMTP host (SMTP gateways only)." example:"relay.example.com"`
	Port             int               `json:"port,omitempty" doc:"SMTP port 1–65535 (SMTP gateways only)." example:"587"`
	TLS              string            `json:"tls,omitempty" doc:"implicit, starttls or none (SMTP gateways only)." example:"starttls"`
	Auth             AuthInput         `json:"auth,omitempty" doc:"SMTP login (SMTP gateways only)."`
	API              *APIConfig        `json:"api,omitempty" doc:"Provider options (API gateways only)."`
	Credentials      *CredentialsInput `json:"credentials,omitempty" doc:"Provider secrets (API gateways only; required there)."`
	EHLOName         string            `json:"ehloName,omitempty" doc:"Host name sent in EHLO (SMTP gateways). Default: the instance EHLO name." example:"relay-out.example.com"`
	LocalAddress     string            `json:"localAddress,omitempty" doc:"Local IP address to bind outgoing SMTP connections to." example:"192.0.2.10"`
	Proxy            string            `json:"proxy,omitempty" doc:"socks5:// or http:// proxy for SMTP connections." example:"socks5://proxy.example.net:1080"`
	Policy           string            `json:"policy,omitempty" doc:"Retry policy: standard or atMostOnce (never resend when the outcome is unknown)." example:"standard"`
	MaxAttempts      *int              `json:"maxAttempts,omitempty" doc:"Maximum send attempts per message (1–100)." example:"5"`
	BackoffBase      string            `json:"backoffBase,omitempty" doc:"Base delay of the exponential retry backoff (Go duration)." example:"30s"`
	IgnoreCertErrors bool              `json:"ignoreCertErrors,omitempty" doc:"Accept an invalid or self-signed server certificate (SMTP gateways; only for trusted internal relays)." example:"false"`
	Enabled          *bool             `json:"enabled,omitempty" doc:"Whether accounts may send through the gateway. Default true." example:"true"`
}

// PatchInput is PATCH /v1/smtp-gateways/{id}.
type PatchInput struct {
	Name             *string           `json:"name,omitempty" doc:"New display name (1–200 characters, unique)." example:"Office relay (backup)"`
	Kind             *string           `json:"kind,omitempty" doc:"Must equal the current kind (the kind cannot change: 409 gateway_kind_immutable)." example:"smtp"`
	Host             *string           `json:"host,omitempty" doc:"New SMTP host." example:"relay2.example.com"`
	Port             *int              `json:"port,omitempty" doc:"New SMTP port." example:"465"`
	TLS              *string           `json:"tls,omitempty" doc:"New transport security." example:"implicit"`
	Auth             *AuthInput        `json:"auth,omitempty" doc:"New SMTP login; send pass only to replace the password."`
	API              *APIConfig        `json:"api,omitempty" doc:"Replaces the provider options."`
	Credentials      *CredentialsInput `json:"credentials,omitempty" doc:"Replaces the provided secrets (others are kept)."`
	EHLOName         *string           `json:"ehloName,omitempty" doc:"Host name sent in EHLO (SMTP gateways). Default: the instance EHLO name." example:"relay-out.example.com"`
	LocalAddress     *string           `json:"localAddress,omitempty" doc:"Local IP address to bind outgoing SMTP connections to." example:"192.0.2.10"`
	Proxy            *string           `json:"proxy,omitempty" doc:"socks5:// or http:// proxy for SMTP connections." example:"socks5://proxy.example.net:1080"`
	Policy           *string           `json:"policy,omitempty" doc:"Retry policy: standard or atMostOnce (never resend when the outcome is unknown)." example:"standard"`
	MaxAttempts      *int              `json:"maxAttempts,omitempty" doc:"Maximum send attempts per message (1–100)." example:"5"`
	ClearMaxAttempts bool              `json:"-"`
	BackoffBase      *string           `json:"backoffBase,omitempty" doc:"Base delay of the exponential retry backoff (Go duration)." example:"30s"`
	IgnoreCertErrors *bool             `json:"ignoreCertErrors,omitempty" doc:"Accept an invalid or self-signed server certificate (SMTP gateways; only for trusted internal relays)." example:"false"`
	Enabled          *bool             `json:"enabled,omitempty" doc:"Whether accounts may send through the gateway. Default true." example:"true"`
}

// AuthView is redacted SMTP auth.
type AuthView struct {
	User        string `json:"user" doc:"SMTP login user." example:"relay@example.com"`
	HasPassword bool   `json:"hasPassword" doc:"True when a password is stored (never returned)." example:"true"`
}

// CredentialsView is redacted API credential flags.
type CredentialsView struct {
	HasAccessKey     bool `json:"hasAccessKey" doc:"An access key is stored." example:"false"`
	HasSecretKey     bool `json:"hasSecretKey" doc:"A secret key is stored." example:"false"`
	HasAPIKey        bool `json:"hasApiKey" doc:"An API key is stored." example:"true"`
	HasWebhookSecret bool `json:"hasWebhookSecret" doc:"A webhook secret is stored (delivery webhooks are accepted)." example:"true"`
}

// View is the API representation.
type View struct {
	ID               string           `json:"id" doc:"SMTP gateway ID (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
	OrganizationID   string           `json:"organizationId" doc:"Organisation that owns the gateway." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name             string           `json:"name" doc:"Display name." example:"Office relay"`
	Kind             string           `json:"kind" doc:"smtp, ses, postmark, mailgun, sendgrid or brevo." example:"smtp"`
	Host             string           `json:"host,omitempty" doc:"SMTP host (SMTP gateways)." example:"relay.example.com"`
	Port             int              `json:"port,omitempty" doc:"SMTP port (SMTP gateways)." example:"587"`
	TLS              string           `json:"tls,omitempty" doc:"Transport security (SMTP gateways)." example:"starttls"`
	Auth             *AuthView        `json:"auth,omitempty" doc:"SMTP login without password (SMTP gateways)."`
	API              *APIConfig       `json:"api,omitempty" doc:"Provider options (API gateways)."`
	Credentials      *CredentialsView `json:"credentials,omitempty" doc:"Which provider secrets are stored (API gateways); the secrets are never returned."`
	WebhookURL       string           `json:"webhookUrl,omitempty" doc:"URL to configure as the delivery / bounce webhook at the provider (API gateways, when EB_CALLBACK_URL is set)." example:"https://emailbridge.example.com/callbacks/gateways/gwy_01m56nt9y6czt3tgr4yj3zpebf"`
	EHLOName         string           `json:"ehloName,omitempty" doc:"Host name sent in EHLO (SMTP gateways). Default: the instance EHLO name." example:"relay-out.example.com"`
	LocalAddress     string           `json:"localAddress,omitempty" doc:"Local IP address to bind outgoing SMTP connections to." example:"192.0.2.10"`
	Proxy            string           `json:"proxy,omitempty" doc:"socks5:// or http:// proxy for SMTP connections." example:"socks5://proxy.example.net:1080"`
	Policy           string           `json:"policy,omitempty" doc:"Retry policy: standard or atMostOnce (never resend when the outcome is unknown)." example:"standard"`
	MaxAttempts      *int             `json:"maxAttempts,omitempty" doc:"Maximum send attempts per message (1–100)." example:"5"`
	BackoffBase      string           `json:"backoffBase,omitempty" doc:"Base delay of the exponential retry backoff (Go duration)." example:"30s"`
	IgnoreCertErrors bool             `json:"ignoreCertErrors,omitempty" doc:"Accept an invalid or self-signed server certificate (SMTP gateways; only for trusted internal relays)." example:"false"`
	Enabled          bool             `json:"enabled" doc:"Whether accounts may send through the gateway. Default true." example:"true"`
	CreatedAt        time.Time        `json:"createdAt" doc:"When the gateway was created." example:"2026-10-10T09:00:00Z"`
	UpdatedAt        time.Time        `json:"updatedAt" doc:"Last change." example:"2026-10-25T13:44:02Z"`
	Version          int64            `json:"version" doc:"Resource version; the ETag is \"v\" followed by this number." example:"2"`
}

// ViewOf redacts a gateway. callbackBase is optional (EB_CALLBACK_URL) for webhookUrl.
func ViewOf(g *Gateway, callbackBase string) View {
	kind := g.EffectiveKind()
	v := View{
		ID: g.ID, OrganizationID: g.OrganizationID, Name: g.Name, Kind: kind,
		EHLOName: g.EHLOName, LocalAddress: g.LocalAddress, Proxy: g.Proxy, Policy: g.Policy,
		MaxAttempts: g.MaxAttempts, BackoffBase: g.BackoffBase, IgnoreCertErrors: g.IgnoreCertErrors,
		Enabled: g.Enabled, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, Version: g.Version,
	}
	if kind == KindSMTP {
		v.Host, v.Port, v.TLS = g.Host, g.Port, g.TLS
		v.Auth = &AuthView{User: g.Auth.User, HasPassword: !g.Auth.Pass.IsZero()}
		return v
	}
	if g.API != nil {
		cp := *g.API
		v.API = &cp
	}
	cv := &CredentialsView{}
	if g.APICreds != nil {
		cv.HasAccessKey = !g.APICreds.AccessKey.IsZero()
		cv.HasSecretKey = !g.APICreds.SecretKey.IsZero()
		cv.HasAPIKey = !g.APICreds.APIKey.IsZero()
		cv.HasWebhookSecret = !g.APICreds.WebhookSecret.IsZero()
	}
	v.Credentials = cv
	if callbackBase != "" {
		v.WebhookURL = webhookURL(callbackBase, g.ID)
	}
	return v
}

func webhookURL(base, id string) string {
	b := strings.TrimRight(base, "/")
	if b == "" {
		return ""
	}
	return b + "/callbacks/gateways/" + id
}

// List is GET /v1/smtp-gateways.
type List struct {
	Data        []View  `json:"data" doc:"The gateways of this page."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6Imd3eV8wMW01Nm50OXk2Y3p0M3RncjR5ajN6cGViZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching gateways (only with page and includeTotal)." example:"2"`
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
	OK           bool     `json:"ok" doc:"True when the connection, TLS and login succeeded." example:"true"`
	LatencyMs    int      `json:"latencyMs" doc:"Time the test took, in milliseconds." example:"356"`
	Capabilities []string `json:"capabilities,omitempty" doc:"SMTP extensions the relay offers." example:"[\"PIPELINING\",\"8BITMIME\",\"SMTPUTF8\",\"SIZE\",\"AUTH\"]"`
	Code         string   `json:"code,omitempty" doc:"Problem code when ok is false (mail_connect_failed, mail_tls_failed, mail_auth_failed, mail_timeout …)." example:"mail_auth_failed"`
	Detail       string   `json:"detail,omitempty" doc:"Server answer or error text when ok is false." example:"535 5.7.8 Authentication credentials invalid"`
}

// DialSettings is the decrypted SMTP target for the submit pool (and verify).
type DialSettings struct {
	ID               string
	OrganizationID   string
	Host             string
	Port             int
	TLS              string
	User, Password   string
	EHLOName         string
	LocalAddress     string
	Proxy            string
	Policy           string
	MaxAttempts      int
	BackoffBase      time.Duration
	IgnoreCertErrors bool
}

// ResolvedAPI is the decrypted API gateway for the submit worker.
type ResolvedAPI struct {
	GatewayID   string
	Kind        string
	Config      ProviderConfig
	Policy      string
	MaxAttempts int
	BackoffBase time.Duration
}
