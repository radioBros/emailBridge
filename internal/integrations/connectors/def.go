// Package connectors holds P8.2 connector manifests (chat, helpdesk).
// The integrations package materialises these into registry Definition values
// so this package never imports integrations (no import cycle).
package connectors

import "context"

// Def is a versioned connector manifest.
type Def struct {
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
	Endpoints    []EndpointTmpl
	Routes       []RouteTmpl
	Settings     map[string]string
	// InstanceOwnerOnly restricts install to instance-owner callers (W37 Sentry).
	InstanceOwnerOnly bool
	// Tester selects a built-in Test hook materialised by integrations.
	Tester Tester
	// TestFunc overrides Tester when set (used by P8.3 HTTP probes).
	TestFunc func(ctx context.Context, in TestInput) (TestResult, error)
}

// OIDCPresetKey is the JSON Schema extension carrying SSO → OIDC form defaults (W37).
const OIDCPresetKey = "x-eb-oidc-preset"

// OIDCPreset is the catalogue payload for "Start from a preset" on the OIDC panel.
type OIDCPreset struct {
	IssuerURLPattern string            `json:"issuerUrlPattern"`
	Scopes           []string          `json:"scopes"`
	GroupClaim       string            `json:"groupClaim,omitempty"`
	ButtonLabel      string            `json:"buttonLabel"`
	Icon             string            `json:"icon,omitempty"`
	ClaimMapping     map[string]string `json:"claimMapping,omitempty"`
	HelpText         string            `json:"helpText,omitempty"`
	RedirectURIPath  string            `json:"redirectURIPath,omitempty"`
}

// RedirectURIPathTemplate is registered at the IdP after the OIDC provider is saved.
const RedirectURIPathTemplate = "/v1/auth/oidc/<provider-id>/callback"

// KindIntegration is the webhook endpoint kind for connector actions (W52).
const KindIntegration = "integration"

// EndpointTmpl creates a webhook endpoint.
type EndpointTmpl struct {
	Name       string
	URL        string
	Kind       string // empty → http; "integration" for CRM/storage actions
	Events     []string
	SinkConfig map[string]string // templates (e.g. instanceId={{instance.id}})
	// PayloadText / PayloadAttachments ask for the message text / attachment list in the deliveries the action
	// receives (CRM logs the body, storage uploads the attachments).
	PayloadText, PayloadAttachments bool
}

// RouteTmpl creates a route targeting EndpointIndex.
type RouteTmpl struct {
	Name          string
	EndpointIndex int
	Filter        string
	Map           string
}

// Tester identifies which Test implementation to attach.
type Tester string

const (
	TesterNone        Tester = ""
	TesterSlack       Tester = "slack"
	TesterTeams       Tester = "teams"
	TesterDiscord     Tester = "discord"
	TesterTelegram    Tester = "telegram"
	TesterMattermost  Tester = "mattermost"
	TesterFeatherDesk Tester = "featherdesk"
	TesterHelpdesk    Tester = "helpdesk"
)

// Categories mirror integrations.Category*.
const (
	CategoryChat     = "chat"
	CategoryHelpdesk = "helpdesk"
)

// Capability flags.
const (
	CapTest            = "test"
	CapInboundCallback = "inboundCallback"
	CapOAuth           = "oauth"
)

// Extra category constants used by P8.3 files in this package.
const (
	CategorySending       = "sending"
	CategoryStorage       = "storage"
	CategorySpam          = "spam"
	CategoryCRM           = "crm"
	CategorySSO           = "sso"
	CategoryObservability = "observability"
)

// Connector ids.
const (
	IDSlack       = "slack"
	IDTeams       = "teams"
	IDDiscord     = "discord"
	IDTelegram    = "telegram"
	IDMattermost  = "mattermost"
	IDFeatherDesk = "featherdesk"
	IDHelpdesk    = "helpdesk"
)

// All returns P8.2 connector manifests sorted by id for stable registration.
func All() []Def {
	return []Def{
		Discord(),
		FeatherDesk(),
		Helpdesk(),
		Mattermost(),
		Slack(),
		Teams(),
		Telegram(),
	}
}

// chatEventFields are shared optional CEL overrides on chat connectors.
func chatEventFields() map[string]any {
	return map[string]any{
		"eventsNewMail": map[string]any{
			"type": "string", "title": "New-mail filter CEL",
			"default": FilterNewMail,
		},
		"eventsDelivery": map[string]any{
			"type": "string", "title": "Delivery-failure filter CEL",
			"default": FilterDelivery,
		},
		"eventsHealth": map[string]any{
			"type": "string", "title": "Account-health filter CEL",
			"default": FilterHealth,
		},
		"eventsQuota": map[string]any{
			"type": "string", "title": "Quota-warning filter CEL",
			"default": FilterQuota,
		},
		"digestMinutes": map[string]any{
			"type": "integer", "title": "Digest window (minutes)", "minimum": 0, "default": 0,
			"description": "Reserved for a scheduled new-mail digest job (0 = per-message alerts only).",
		},
	}
}

// Chat alert CEL defaults (P8.2 §1).
const (
	FilterNewMail  = `type == "message.received"`
	FilterDelivery = `type in ["message.failed", "message.bounced", "message.delivery_attempt_failed"]`
	FilterHealth   = `type in ["account.auth_failed", "account.state_changed", "account.connect_failed"]`
	FilterQuota    = `type in ["account.connect_failed", "account.state_changed"]`
)

func chatRoutes(maps chatMaps) []RouteTmpl {
	return []RouteTmpl{
		{Name: "New mail", EndpointIndex: 0, Filter: "{{config.eventsNewMail}}", Map: maps.NewMail},
		{Name: "Delivery failures", EndpointIndex: 0, Filter: "{{config.eventsDelivery}}", Map: maps.Delivery},
		{Name: "Account health", EndpointIndex: 0, Filter: "{{config.eventsHealth}}", Map: maps.Health},
		{Name: "Quota warnings", EndpointIndex: 0, Filter: "{{config.eventsQuota}}", Map: maps.Quota},
	}
}

type chatMaps struct {
	NewMail, Delivery, Health, Quota string
}
