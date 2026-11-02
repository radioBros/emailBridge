package configcode

import (
	"time"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// API / document constants.
const (
	APIVersion = "emailbridge.dev/v1"
	KindOrg    = "OrganizationConfig"
	JobKind    = "config.apply"

	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"

	KindSettings = "settings"
	KindSwitch   = "switch"
	KindEndpoint = "webhookEndpoint"
	KindRoute    = "route"
	KindAPIKey   = "apiKey"
	maxPerKind   = 500
)

// Document is the declarative organisation config (YAML / JSON).
type Document struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion" doc:"Document format version: emailbridge.dev/v1 (default when omitted on plan / apply)." example:"emailbridge.dev/v1"`
	Kind       string   `json:"kind" yaml:"kind" doc:"Always OrganizationConfig (default when omitted)." example:"OrganizationConfig"`
	Metadata   Metadata `json:"metadata" yaml:"metadata" doc:"Which organisation the document describes."`
	Spec       Spec     `json:"spec" yaml:"spec" doc:"The managed resources. Resources not listed are left alone unless prune is true."`
}

// Metadata names the organisation by slug.
type Metadata struct {
	Organization string `json:"organization" yaml:"organization" doc:"Slug of the organisation." example:"acme-legal"`
}

// Spec holds managed resources. Reserved slices stay empty until later phases.
type Spec struct {
	Settings         map[string]any   `json:"settings,omitempty" yaml:"settings,omitempty" doc:"Organisation-level runtime settings (key → value), as in PATCH /v1/settings." example:"{\"webhooks.autoDisableAfter\":\"5d\",\"attachments.signedUrlTtl\":\"15m\"}"`
	Switches         map[string]any   `json:"switches,omitempty" yaml:"switches,omitempty" doc:"Organisation-level feature switches (name → value)." example:"{\"sandbox.enabled\":false,\"tracking.opens\":true}"`
	WebhookEndpoints []EndpointSpec   `json:"webhookEndpoints,omitempty" yaml:"webhookEndpoints,omitempty" doc:"Webhook endpoints, identified by name. Secrets must be ${secret:NAME} references to this organisation's settings.env secrets, never inline values."`
	Routes           []RouteSpec      `json:"routes,omitempty" yaml:"routes,omitempty" doc:"Routes, identified by name; endpoint names the target webhook endpoint."`
	APIKeys          []APIKeySpec     `json:"apiKeys,omitempty" yaml:"apiKeys,omitempty" doc:"API key metadata, identified by name. Tokens are never in the document or plan; create apply returns each new token once on the action ref."`
	Templates        []map[string]any `json:"templates,omitempty" yaml:"templates,omitempty" doc:"Not yet managed by config-as-code: a non-empty list is rejected with 422." example:"[]"`
	SMTPGateways     []map[string]any `json:"smtpGateways,omitempty" yaml:"smtpGateways,omitempty" doc:"Not yet managed by config-as-code: a non-empty list is rejected with 422." example:"[]"`
	SuppressionLists []map[string]any `json:"suppressionLists,omitempty" yaml:"suppressionLists,omitempty" doc:"Not yet managed by config-as-code: a non-empty list is rejected with 422." example:"[]"`
}

// EndpointSpec is one webhookEndpoints entry (identity = name).
type EndpointSpec struct {
	Name       string                  `json:"name" yaml:"name" doc:"Endpoint name (unique; the identity used by plan)." example:"crm-inbound"`
	Kind       string                  `json:"kind,omitempty" yaml:"kind,omitempty" doc:"Endpoint kind (webhook by default, or a sink kind)." example:"webhook"`
	Enabled    *bool                   `json:"enabled,omitempty" yaml:"enabled,omitempty" doc:"Whether deliveries are made." example:"true"`
	URL        string                  `json:"url,omitempty" yaml:"url,omitempty" doc:"Delivery URL (https)." example:"https://crm.example.com/hooks/emailbridge"`
	Events     []string                `json:"events,omitempty" yaml:"events,omitempty" doc:"Event types to deliver." example:"[\"message.received\",\"message.bounced\"]"`
	Secret     string                  `json:"secret,omitempty" yaml:"secret,omitempty" doc:"Signing secret as a ${secret:NAME} reference (resolved from the organisation's own settings.env secrets); inline values are rejected." example:"${secret:CRM_WEBHOOK_SECRET}"`
	Filter     *webhook.EndpointFilter `json:"filter,omitempty" yaml:"filter,omitempty" doc:"Only deliver events of these accounts / mailboxes."`
	Payload    *webhook.PayloadOptions `json:"payload,omitempty" yaml:"payload,omitempty" doc:"Payload options (text, headers, attachments …)."`
	Retry      *webhook.RetryOptions   `json:"retry,omitempty" yaml:"retry,omitempty" doc:"Retry policy."`
	Delivery   *webhook.DeliveryUpdate `json:"delivery,omitempty" yaml:"delivery,omitempty" doc:"Delivery options (ordering, concurrency, content type …)."`
	Security   *webhook.SecurityWrite  `json:"security,omitempty" yaml:"security,omitempty" doc:"Signature mode and TLS options."`
	Headers    []webhook.WebhookHeader `json:"headers,omitempty" yaml:"headers,omitempty" doc:"Extra HTTP headers sent with each delivery."`
	Switches   map[string]any          `json:"switches,omitempty" yaml:"switches,omitempty" doc:"Endpoint-level switches (name → value)." example:"{\"webhook.mtls\":false}"`
	SinkConfig map[string]any          `json:"sinkConfig,omitempty" yaml:"sinkConfig,omitempty" doc:"Configuration of a sink endpoint kind (kind-specific keys)." example:"{\"bucket\":\"acme-mail-events\"}"`
}

// RouteSpec is one routes entry; Endpoint is the endpoint name.
type RouteSpec struct {
	Name     string         `json:"name" yaml:"name" doc:"Route name (unique)." example:"invoices-to-crm"`
	Endpoint string         `json:"endpoint" yaml:"endpoint" doc:"Name of the target webhook endpoint (in this document or live)." example:"crm-inbound"`
	Enabled  *bool          `json:"enabled,omitempty" yaml:"enabled,omitempty" doc:"Whether the route is evaluated." example:"true"`
	Filter   string         `json:"filter,omitempty" yaml:"filter,omitempty" doc:"CEL filter expression; the route runs when it is true." example:"event.type == \"message.received\" && event.data.subject.contains(\"Invoice\")"`
	Map      string         `json:"map,omitempty" yaml:"map,omitempty" doc:"Optional CEL expression that reshapes the payload." example:"{\"from\": event.data.from.address, \"subject\": event.data.subject}"`
	Order    *int           `json:"order,omitempty" yaml:"order,omitempty" doc:"Evaluation order (lower first)." example:"10"`
	Secret   string         `json:"secret,omitempty" yaml:"secret,omitempty" doc:"Route signing secret as a ${secret:NAME} reference." example:"${secret:CRM_ROUTE_SECRET}"`
	Headers  map[string]any `json:"headers,omitempty" yaml:"headers,omitempty" doc:"Extra HTTP headers (name → value)." example:"{\"X-Source\":\"emailbridge\"}"`
	Payload  map[string]any `json:"payload,omitempty" yaml:"payload,omitempty" doc:"Payload options for this route." example:"{\"text\":\"plain\",\"attachments\":false}"`
	Builder  map[string]any `json:"builder,omitempty" yaml:"builder,omitempty" doc:"Visual-builder state of the route, kept for the admin UI." example:"{\"conditions\":[{\"field\":\"subject\",\"op\":\"contains\",\"value\":\"Invoice\"}]}"`
}

// APIKeySpec is API key metadata (never a token).
type APIKeySpec struct {
	Name         string                `json:"name" yaml:"name" doc:"Key name (unique; the identity used by plan)." example:"crm-sync"`
	Scopes       []string              `json:"scopes,omitempty" yaml:"scopes,omitempty" doc:"Granted scopes." example:"[\"accounts:read\",\"messages:read\"]"`
	AccountID    string                `json:"accountId,omitempty" yaml:"accountId,omitempty" doc:"Bind the key to one account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Restrictions *apikeys.Restrictions `json:"restrictions,omitempty" yaml:"restrictions,omitempty" doc:"Client IP / referrer restrictions."`
	RateLimit    *apikeys.RateLimit    `json:"rateLimit,omitempty" yaml:"rateLimit,omitempty" doc:"Per-key rate limit hint."`
	ExpiresAt    *time.Time            `json:"expiresAt,omitempty" yaml:"expiresAt,omitempty" doc:"Expiry (RFC 3339)." example:"2027-10-27T00:00:00Z"`
}

// PlanRequest is POST /v1/config/plan.
type PlanRequest struct {
	Document Document `json:"document" doc:"The desired configuration (JSON form of the YAML document)."`
	Prune    bool     `json:"prune,omitempty" doc:"Also delete live endpoints, routes and API keys that the document does not list. Default false." example:"false"`
}

// ApplyRequest is POST /v1/config/apply.
type ApplyRequest struct {
	Document Document `json:"document" doc:"The same document that was planned."`
	Prune    bool     `json:"prune,omitempty" doc:"The same prune flag that was planned." example:"false"`
	PlanHash string   `json:"planHash" doc:"planHash from POST /v1/config/plan; apply is refused with 409 config_drift when the live configuration changed since." example:"sha256:5f2c8e1a9b7d4c3e6f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6"`
}

// Plan is the diff result.
type Plan struct {
	PlanHash string   `json:"planHash" doc:"Hash of prune + actions; pass it to POST /v1/config/apply." example:"sha256:5f2c8e1a9b7d4c3e6f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6"`
	Prune    bool     `json:"prune" doc:"Whether deletions of unlisted resources are included." example:"false"`
	Actions  []Action `json:"actions" doc:"Steps in apply order; empty when live already matches."`
}

// Action is one create/update/delete step.
type Action struct {
	Op      string         `json:"op" doc:"create, update or delete." example:"update"`
	Kind    string         `json:"kind" doc:"settings, switch, webhookEndpoint, route or apiKey." example:"webhookEndpoint"`
	Name    string         `json:"name,omitempty" doc:"Name of the resource (settings: organization; switch: the switch name)." example:"crm-inbound"`
	ID      string         `json:"id,omitempty" doc:"ID of the live resource (update / delete)." example:"whe_01m5h2pme18h2xzbq4pdypnrv8"`
	Version int64          `json:"version,omitempty" doc:"Live version the action was planned against." example:"4"`
	Changes map[string]any `json:"changes,omitempty" doc:"Field → {before, after}; secret values show only (changed) / (unchanged)." example:"{\"url\":{\"before\":\"https://crm.example.com/hooks/old\",\"after\":\"https://crm.example.com/hooks/emailbridge\"},\"secret\":\"(changed)\"}"`
}

// ApplyResult is the config.apply job result.
type ApplyResult struct {
	PlanHash  string         `json:"planHash"`
	Applied   []ActionRef    `json:"applied"`
	StoppedAt *StoppedAction `json:"stoppedAt,omitempty"`
}

// ActionRef identifies a completed action.
type ActionRef struct {
	Op    string `json:"op"`
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	ID    string `json:"id,omitempty"`
	Token string `json:"token,omitempty" doc:"Plaintext API key, only on apiKey create; store it, it is never shown again."`
}

// StoppedAction is where apply halted.
type StoppedAction struct {
	Op    string `json:"op"`
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error"`
}

// liveSnapshot is the normalised live state used by plan/diff.
type liveSnapshot struct {
	OrgSlug   string
	OrgID     string
	Settings  map[string]any // org-owned runtime values only
	Switches  map[string]any // org-owned switch values only
	SettingsV int64
	Endpoints map[string]liveEndpoint // by name
	Routes    map[string]liveRoute
	APIKeys   map[string]liveAPIKey
}

type liveEndpoint struct {
	ID        string
	Version   int64
	Spec      EndpointSpec
	HasSecret bool
}

type liveRoute struct {
	ID           string
	Version      int64
	EndpointName string
	Spec         RouteSpec
	HasSecret    bool
}

type liveAPIKey struct {
	ID      string
	Version int64
	Spec    APIKeySpec
}
