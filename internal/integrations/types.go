package integrations

import (
	"context"
	"net/http"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collection is the MongoDB collection for integration instances.
const Collection = "integrationInstances"

// Categories (S16).
const (
	CategoryChat          = "chat"
	CategoryHelpdesk      = "helpdesk"
	CategoryAutomation    = "automation"
	CategorySending       = "sending"
	CategorySpam          = "spam"
	CategoryStorage       = "storage"
	CategoryCRM           = "crm"
	CategorySSO           = "sso"
	CategoryObservability = "observability"
)

// Capability flags.
const (
	CapTest            = "test"
	CapInboundCallback = "inboundCallback"
	CapOAuth           = "oauth"
)

// Connector IDs (P8.1 references + P8.2 catalogue).
const (
	TypeSlack       = "slack"
	TypeZapier      = "zapier"
	TypeRspamd      = "rspamd"
	TypeTeams       = "teams"
	TypeDiscord     = "discord"
	TypeTelegram    = "telegram"
	TypeMattermost  = "mattermost"
	TypeFeatherDesk = "featherdesk"
	TypeHelpdesk    = "helpdesk"
)

// Definition is a versioned connector registered in-process.
type Definition struct {
	ID           string
	Version      string
	Name         string
	Category     string
	Icon         string
	Color        string
	Description  string
	DocsPath     string
	ConfigSchema map[string]any
	SecretFields []string
	Capabilities []string
	Resources    ResourceTemplates
	// InstanceOwnerOnly restricts install to instance-owner callers (W37 Sentry).
	InstanceOwnerOnly bool
	// Test runs a connector-specific check. Nil → test returns ok with a "not supported" detail.
	Test func(ctx context.Context, in TestInput) (TestResult, error)
}

// ResourceTemplates describe what install creates (placeholders {{config.x}} / {{secrets.x}}).
type ResourceTemplates struct {
	WebhookEndpoints []EndpointTemplate
	Routes           []RouteTemplate
	Settings         map[string]string // setting key → value template
}

// EndpointTemplate creates a webhook endpoint.
type EndpointTemplate struct {
	Name       string
	URL        string // template (HTTP)
	Kind       string // empty → http; "integration" for CRM/storage
	Events     []string
	SinkConfig map[string]string // templates
	// PayloadText / PayloadAttachments: see connectors.EndpointTmpl.
	PayloadText, PayloadAttachments bool
}

// RouteTemplate creates a route targeting the Nth created endpoint (EndpointIndex).
type RouteTemplate struct {
	Name          string
	EndpointIndex int
	Filter        string
	Map           string
	EventsHint    []string // documentation only; routes use CEL filter
}

// TestInput is passed to Definition.Test.
type TestInput struct {
	Config  map[string]any
	Secrets map[string]string
	Client  *http.Client
}

// TestResult is POST …/test.
type TestResult struct {
	OK        bool   `json:"ok" doc:"The connector check passed" example:"true"`
	LatencyMs int64  `json:"latencyMs" doc:"Time the check took in milliseconds" example:"312"`
	Detail    string `json:"detail,omitempty" doc:"What was checked, or why it failed (redacted)" example:"Slack accepted the test message (HTTP 200)."`
}

// CreatedResources lists resources owned by an instance.
type CreatedResources struct {
	Endpoints []string `bson:"endpoints,omitempty" json:"endpoints,omitempty" doc:"Webhook endpoints the install created (whe_…)"`
	Routes    []string `bson:"routes,omitempty" json:"routes,omitempty" doc:"Routes the install created (rte_…)"`
	Settings  []string `bson:"settings,omitempty" json:"settings,omitempty" doc:"Setting keys the install set"`
	Gateways  []string `bson:"gateways,omitempty" json:"gateways,omitempty" doc:"SMTP gateways the install created (gwy_…)"`
}

// Instance is a stored integrationInstances document.
type Instance struct {
	ID                string                            `bson:"_id" json:"-"`
	OrganizationID    string                            `bson:"organizationId" json:"-"`
	Type              string                            `bson:"type"`
	DefinitionVersion string                            `bson:"definitionVersion"`
	Name              string                            `bson:"name"`
	Config            map[string]any                    `bson:"config,omitempty"`
	Secrets           map[string]crypto.EncryptedString `bson:"secrets,omitempty"`
	CreatedResources  CreatedResources                  `bson:"createdResources"`
	Enabled           bool                              `bson:"enabled"`
	CreatedAt         time.Time                         `bson:"createdAt"`
	UpdatedAt         time.Time                         `bson:"updatedAt"`
	Version           int64                             `bson:"version"`
	CreatedBy         string                            `bson:"createdBy,omitempty"`
	// Digest cursor (chat connectors with digestMinutes > 0).
	LastDigestAt         *time.Time `bson:"lastDigestAt,omitempty" json:"lastDigestAt,omitempty"`
	LastDigestInternalAt *time.Time `bson:"lastDigestInternalAt,omitempty" json:"lastDigestInternalAt,omitempty"`
	// Runtime action status (CRM/storage, W52).
	LastActionAt     *time.Time `bson:"lastActionAt,omitempty" json:"-"`
	LastActionOK     bool       `bson:"lastActionOK,omitempty" json:"-"`
	LastActionError  string     `bson:"lastActionError,omitempty" json:"-"`
	LastActionDetail string     `bson:"lastActionDetail,omitempty" json:"-"`
}

// View is the secret-safe API shape.
type View struct {
	ID                string           `json:"id" doc:"Integration instance ID (int_…)" example:"int_01m5dzndt02er7qagrrs531p8g"`
	OrganizationID    string           `json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Type              string           `json:"type" doc:"Connector ID from the catalogue" example:"slack"`
	DefinitionVersion string           `json:"definitionVersion" doc:"Connector version the instance was installed with" example:"1.1.0"`
	Name              string           `json:"name" doc:"Display name" example:"Slack: #support-mail"`
	Config            map[string]any   `json:"config" doc:"Non-secret configuration (shape: the connector's configSchema)"`
	SecretsSet        map[string]bool  `json:"secretsSet" doc:"Which secret fields have a stored value (values are never returned)"`
	CreatedResources  CreatedResources `json:"createdResources" doc:"Resources the instance owns; uninstall deletes them"`
	Enabled           bool             `json:"enabled" doc:"The instance and its resources are active" example:"true"`
	CreatedAt         time.Time        `json:"createdAt" doc:"When it was installed" example:"2026-10-24T09:00:00Z"`
	UpdatedAt         time.Time        `json:"updatedAt" doc:"When it last changed" example:"2026-10-26T11:30:00Z"`
	Version           int64            `json:"version" doc:"Document version; the ETag" example:"2"`
	LastActionAt      *time.Time       `json:"lastActionAt,omitempty" doc:"Last connector action (CRM / storage connectors)" example:"2026-10-27T09:14:25Z"`
	LastActionOK      *bool            `json:"lastActionOK,omitempty" doc:"The last action succeeded" example:"true"`
	LastActionError   string           `json:"lastActionError,omitempty" doc:"Error of the last failed action (redacted)" example:"HTTP 401 from the CRM: invalid API token"`
	LastActionDetail  string           `json:"lastActionDetail,omitempty" doc:"Detail of the last action (redacted)" example:"HTTP 201 from the CRM"`
	GatewayID         string           `json:"gatewayId,omitempty" doc:"SMTP gateway created by a sending connector (gwy_…)" example:"gwy_01m5e3wxr0k0k8597dg8a9k9m3"`
}

// CatalogueItem is one entry of GET /v1/integrations.
type CatalogueItem struct {
	ID             string         `json:"id" doc:"Connector ID, used as {type} in install" example:"slack"`
	Version        string         `json:"version" doc:"Connector version" example:"1.1.0"`
	Name           string         `json:"name" doc:"Display name" example:"Slack"`
	Category       string         `json:"category" doc:"chat, helpdesk, automation, sending, spam, storage, crm, sso or observability" example:"chat"`
	Icon           string         `json:"icon,omitempty" doc:"MDI icon name" example:"mdi-slack"`
	Color          string         `json:"color,omitempty" doc:"Brand colour" example:"#4A154B"`
	Description    string         `json:"description,omitempty" doc:"What it does and which data leaves EmailBridge" example:"Post mail alerts to Slack (Block Kit): new mail, delivery failures, account health, and quota warnings."`
	DocsPath       string         `json:"docsPath,omitempty" doc:"Documentation page" example:"/docs/integrations/slack"`
	ConfigSchema   map[string]any `json:"configSchema" doc:"JSON Schema of config (and secrets) for install"`
	SecretFields   []string       `json:"secretFields" doc:"Config fields that are secrets (send them in secrets)"`
	Capabilities   []string       `json:"capabilities" doc:"test, inboundCallback and/or oauth"`
	InstalledCount int            `json:"installedCount" doc:"Instances in this organisation" example:"1"`
	InstanceIDs    []string       `json:"instanceIds,omitempty" doc:"IDs of those instances (int_…)"`
	RuntimeHint    string         `json:"runtimeHint,omitempty" doc:"Runtime note, e.g. an integration that needs an instance setting" example:"Needs EB_SENTRY_DSN on the instance."`
}

// Catalogue is GET /v1/integrations.
type Catalogue struct {
	Data []CatalogueItem `json:"data" doc:"Connectors of the catalogue."`
}

// List is GET /v1/integration-instances.
type List struct {
	Data        []View  `json:"data" doc:"The instances of this page."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6ImludF8wMW01ZHpuZHQwMmVyN3FhZ3JyczUzMXA4ZyJ9"`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching instances (only with page and includeTotal)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

// InstallInput is POST /v1/integrations/{type}/install.
type InstallInput struct {
	Name    string            `json:"name,omitempty" doc:"Display name (default: the connector name; at most 120 characters)" example:"Slack: #support-mail"`
	Config  map[string]any    `json:"config" doc:"Configuration, validated against the connector's configSchema"`
	Secrets map[string]string `json:"secrets,omitempty" doc:"Secret fields (secretFields), stored encrypted and never returned"`
	RunTest bool              `json:"runTest,omitempty" doc:"Run the connector test right after installing (audited as integration.tested)" example:"true"`
}

// PatchInput is PATCH /v1/integration-instances/{id}.
type PatchInput struct {
	Name    *string           `json:"name,omitempty" doc:"New display name" example:"Slack: #mail-alerts"`
	Config  map[string]any    `json:"config,omitempty" doc:"Configuration keys to change (merged; validated against configSchema)"`
	Secrets map[string]string `json:"secrets,omitempty" doc:"Secret fields to replace"`
	Enabled *bool             `json:"enabled,omitempty" doc:"Enable or disable the instance and the endpoints and routes it created" example:"false"`
}

// ListFilter is GET /v1/integration-instances query.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Q            string
	Type         string
}

// CatalogueFilter is GET /v1/integrations query.
type CatalogueFilter struct {
	Q        string
	Category string
}

func viewOf(inst *Instance, secretFields []string) View {
	set := map[string]bool{}
	for _, f := range secretFields {
		if inst.Secrets != nil {
			if es, ok := inst.Secrets[f]; ok && !es.IsZero() {
				set[f] = true
			}
		}
	}
	cfg := inst.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	v := View{
		ID: inst.ID, OrganizationID: inst.OrganizationID, Type: inst.Type,
		DefinitionVersion: inst.DefinitionVersion, Name: inst.Name, Config: cfg,
		SecretsSet: set, CreatedResources: inst.CreatedResources, Enabled: inst.Enabled,
		CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt, Version: inst.Version,
		LastActionError: inst.LastActionError, LastActionDetail: inst.LastActionDetail,
	}
	if inst.LastActionAt != nil {
		v.LastActionAt = inst.LastActionAt
		ok := inst.LastActionOK
		v.LastActionOK = &ok
	}
	if len(inst.CreatedResources.Gateways) > 0 {
		v.GatewayID = inst.CreatedResources.Gateways[0]
	}
	return v
}
