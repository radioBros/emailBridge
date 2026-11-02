// Package webhook owns HTTP webhook endpoints, event routing, signed delivery, retries, circuit breakers,
// delivery history and replay (S7).
package webhook

import (
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

const (
	EndpointsCollection  = "webhookEndpoints"
	DeliveriesCollection = "deliveries"

	KindHTTP        = "http"
	KindIntegration = "integration" // W52: CRM/storage connector actions

	StatePending      = "pending"
	StateDelivering   = "delivering"
	StateRetrying     = "retrying"
	StateSucceeded    = "succeeded"
	StateFailed       = "failed"
	StateDeadLettered = "deadLettered"
	StateCancelled    = "cancelled"

	OrderingStrict     = "strict"
	OrderingBestEffort = "bestEffort"

	SignatureStandard = "standard"
	SignatureBodyOnly = "bodyOnly"
)

var finalStates = []string{StateSucceeded, StateFailed, StateDeadLettered, StateCancelled}

// Endpoint is the stored webhookEndpoints document.
type Endpoint struct {
	ID               string          `bson:"_id" json:"id"`
	OrganizationID   string          `bson:"organizationId" json:"organizationId"`
	Name             string          `bson:"name" json:"name"`
	Kind             string          `bson:"kind" json:"kind" enum:"http,nats,kafka,sqs,sns,pubsub,integration"`
	Enabled          bool            `bson:"enabled" json:"enabled"`
	URL              string          `bson:"url,omitempty" json:"url,omitempty"`
	SinkConfig       map[string]any  `bson:"sinkConfig,omitempty" json:"sinkConfig,omitempty"`
	Events           []string        `bson:"events" json:"events"`
	Filter           EndpointFilter  `bson:"filter,omitempty" json:"filter,omitempty"`
	Headers          []StoredHeader  `bson:"headers,omitempty" json:"-"`
	Payload          PayloadOptions  `bson:"payload,omitempty" json:"payload,omitempty"`
	Secrets          []StoredSecret  `bson:"secrets,omitempty" json:"-"`
	Security         SecurityOptions `bson:"security" json:"security"`
	Retry            RetryOptions    `bson:"retry" json:"retry"`
	Delivery         DeliveryOptions `bson:"delivery" json:"delivery"`
	SchemaVersions   map[string]int  `bson:"schemaVersions,omitempty" json:"-"` // internal (S1/S3); not API-visible
	DisabledReason   *DisabledReason `bson:"disabledReason,omitempty" json:"disabledReason,omitempty"`
	Breaker          Breaker         `bson:"breaker" json:"breaker"`
	Switches         map[string]any  `bson:"switches,omitempty" json:"switches,omitempty"`
	Rule             *EndpointRule   `bson:"rule,omitempty" json:"rule,omitempty"` // W73 CEL filter/map
	FailureStartedAt *time.Time      `bson:"failureStartedAt,omitempty" json:"-"`
	CreatedAt        time.Time       `bson:"createdAt" json:"createdAt"`
	UpdatedAt        time.Time       `bson:"updatedAt" json:"updatedAt"`
	Version          int64           `bson:"version" json:"version"`
}

// EndpointRule is optional CEL filter/map on the endpoint (W73).
type EndpointRule struct {
	Filter         string         `bson:"filter,omitempty" json:"filter,omitempty" doc:"CEL filter evaluated for every event the endpoint subscribes to (same language and helpers as routes). With a rule the endpoint no longer receives events through routes" example:"type == \"message.received\" && fromDomain() == \"example.org\""`
	Map            string         `bson:"map,omitempty" json:"map,omitempty" doc:"Optional CEL expression whose result replaces the delivered body" example:"{\"messageId\": data.messageId, \"subject\": subject(), \"from\": from()}"`
	Builder        map[string]any `bson:"builder,omitempty" json:"builder,omitempty" doc:"No-code builder model the filter was compiled from"`
	CurrentVersion int            `bson:"currentVersion,omitempty" json:"currentVersion,omitempty" doc:"Active rule version (see …/rule/versions)" example:"2"`
}

// HasCEL reports whether the endpoint opted into rule mode (no double-delivery with routes).
func (r *EndpointRule) HasCEL() bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Filter) != "" || strings.TrimSpace(r.Map) != "" || r.Builder != nil
}

// EndpointFilter is the direct endpoint account/mailbox filter.
type EndpointFilter struct {
	AccountIDs   []string `bson:"accountIds,omitempty" json:"accountIds,omitempty" doc:"Only events of these accounts (acc_…); empty = all accounts"`
	MailboxPaths []string `bson:"mailboxPaths,omitempty" json:"mailboxPaths,omitempty" doc:"Only message and mailbox events of these folders, e.g. INBOX; empty = all folders"`
}

// StoredHeader is an encrypted custom request header.
type StoredHeader struct {
	Name   string                 `bson:"name"`
	Value  crypto.EncryptedString `bson:"value"`
	Secret bool                   `bson:"secret"`
}

// WebhookHeader is the write/read API representation of a custom header.
type WebhookHeader struct {
	Name     string `json:"name" doc:"Header name; reserved names (Content-Type, User-Agent, webhook-*, eb-*, traceparent) are refused" example:"X-Api-Key"`
	Value    string `json:"value,omitempty" doc:"Header value; write only for secret headers (never returned), returned for the others" example:"crm-ingest-7f3c2b9e"`
	Secret   bool   `json:"secret,omitempty" doc:"Treat the value as a secret: stored encrypted and never returned" example:"true"`
	HasValue bool   `json:"hasValue,omitempty" doc:"Read only: a value is stored (secret headers)" example:"true"`
}

// StoredSecret is one active or overlap signing secret.
type StoredSecret struct {
	ID        string                 `bson:"id"`
	Value     crypto.EncryptedString `bson:"value"`
	Preview   string                 `bson:"preview"`
	CreatedAt time.Time              `bson:"createdAt"`
	ExpiresAt *time.Time             `bson:"expiresAt,omitempty"`
}

// PayloadOptions select which optional event data reaches an endpoint.
type PayloadOptions struct {
	Text           string   `bson:"text,omitempty" json:"text,omitempty" enum:"none,plain,html,both" doc:"Which message text reaches the endpoint in message.received: none (default), plain, html or both" example:"plain"`
	TextMax        int      `bson:"textMax,omitempty" json:"textMax,omitempty" doc:"Maximum text size in bytes before truncation (default 65536)" example:"65536"`
	Headers        []string `bson:"headers,omitempty" json:"headers,omitempty" doc:"Message headers to include in data.headers (lower case; * = all)"`
	Attachments    bool     `bson:"attachments,omitempty" json:"attachments,omitempty" doc:"Include attachment metadata (data.attachments)" example:"true"`
	AttachmentMax  int      `bson:"attachmentMax,omitempty" json:"attachmentMax,omitempty" doc:"Maximum attachments listed (0 = all)" example:"20"`
	WebSafeHTML    bool     `bson:"webSafeHtml,omitempty" json:"webSafeHtml,omitempty" doc:"Include data.webSafeHtml (sanitised HTML body)" example:"false"`
	CalendarEvents bool     `bson:"calendarEvents,omitempty" json:"calendarEvents,omitempty" doc:"Include data.calendarEvents (parsed invitations)" example:"true"`
	ReplyText      bool     `bson:"replyText,omitempty" json:"replyText,omitempty" doc:"Include data.replyText (new text without quoted history)" example:"true"`
	// AIEnrichment asks the router to add data.ai on message.received (fail-open, W52).
	AIEnrichment bool `bson:"aiEnrichment,omitempty" json:"aiEnrichment,omitempty" doc:"Add data.ai (summary, classification) on message.received; fail-open, needs the AI integration" example:"false"`
}

// SecurityOptions configure request signatures and optional mTLS.
type SecurityOptions struct {
	SignatureMode string       `bson:"signatureMode" json:"signatureMode" enum:"standard,bodyOnly" doc:"standard: Standard Webhooks headers webhook-id, webhook-timestamp, webhook-signature over id.timestamp.body; bodyOnly: eb-signature over the body only" example:"standard"`
	TLS           *EndpointTLS `bson:"tls,omitempty" json:"tls,omitempty" doc:"Client certificate for mTLS"`
}

// SecurityView is the API shape for security (tls key never returned in full).
type SecurityView struct {
	SignatureMode string    `json:"signatureMode" enum:"standard,bodyOnly" doc:"standard (Standard Webhooks: webhook-id, webhook-timestamp, webhook-signature) or bodyOnly (eb-signature over the body)" example:"standard"`
	TLS           *TLSInput `json:"tls,omitempty" doc:"Client certificate for mTLS (the private key is never returned)"`
}

// RetryOptions configure delivery attempts.
type RetryOptions struct {
	MaxAttempts   int     `bson:"maxAttempts" json:"maxAttempts" doc:"Attempts before a delivery is dead-lettered (default 10, or the webhook.retryDefaults setting)" example:"10"`
	BackoffBase   string  `bson:"backoffBase" json:"backoffBase" doc:"First retry delay as a Go duration; doubles per attempt (default 5s)" example:"5s"`
	BackoffJitter float64 `bson:"backoffJitter" json:"backoffJitter" doc:"Random jitter as a fraction of the delay, 0-1 (default 0.2)" example:"0.2"`
	Timeout       string  `bson:"timeout" json:"timeout" doc:"Per-attempt request timeout as a Go duration (default 30s)" example:"30s"`
}

// DeliveryOptions configure ordering and endpoint isolation.
type DeliveryOptions struct {
	Ordering         string `bson:"ordering" json:"ordering" enum:"strict,bestEffort" doc:"strict: one delivery at a time per account and endpoint, in event order; bestEffort: parallel" example:"strict"`
	MaxConcurrency   int    `bson:"maxConcurrency" json:"maxConcurrency" doc:"Parallel deliveries to this endpoint (default 8)" example:"8"`
	ContentType      string `bson:"contentType" json:"contentType" enum:"json,cloudevents" doc:"json: Content-Type application/json; cloudevents: application/cloudevents+json" example:"json"`
	RetryOn4xx       bool   `bson:"retryOn4xx" json:"retryOn4xx" doc:"Retry every 4xx answer too. By default 408, 425, 429 and 5xx are retried, 410 disables the endpoint and other 4xx fail at once" example:"false"`
	AutoDisableAfter string `bson:"autoDisableAfter,omitempty" json:"autoDisableAfter,omitempty" doc:"Disable the endpoint after failing continuously this long (Go duration, default 120h)" example:"120h0m0s"`
}

// DeliveryUpdate is a partial PATCH of delivery options.
type DeliveryUpdate struct {
	Ordering         *string `json:"ordering,omitempty" enum:"strict,bestEffort" doc:"strict or bestEffort" example:"bestEffort"`
	MaxConcurrency   *int    `json:"maxConcurrency,omitempty" doc:"Parallel deliveries to this endpoint" example:"4"`
	ContentType      *string `json:"contentType,omitempty" enum:"json,cloudevents" doc:"json or cloudevents" example:"cloudevents"`
	RetryOn4xx       *bool   `json:"retryOn4xx,omitempty" doc:"Retry 4xx answers too" example:"false"`
	AutoDisableAfter *string `json:"autoDisableAfter,omitempty" doc:"Disable after failing continuously this long (Go duration)" example:"72h"`
}

// DisabledReason records why an endpoint was switched off.
type DisabledReason struct {
	Code string    `bson:"code" json:"code" doc:"continuous_failure (failed for autoDisableAfter) or http_410 (the endpoint answered 410 Gone)" example:"continuous_failure"`
	At   time.Time `bson:"at" json:"at" doc:"When the endpoint was disabled" example:"2026-10-26T03:12:40Z"`
}

// Breaker is the persisted circuit-breaker state. Samples are bounded to the last five minutes.
type Breaker struct {
	State         string          `bson:"state" json:"state" enum:"closed,open,halfOpen" doc:"Circuit breaker: closed (normal), open (deliveries paused after repeated failures) or halfOpen (probing)" example:"closed"`
	OpenedAt      *time.Time      `bson:"openedAt,omitempty" json:"openedAt,omitempty" doc:"When the breaker opened" example:"2026-10-26T02:58:10Z"`
	Failures      int             `bson:"failures" json:"failures" doc:"Failures in the last five minutes" example:"0"`
	OpenCount     int             `bson:"openCount,omitempty" json:"-"`
	NextProbeAt   *time.Time      `bson:"nextProbeAt,omitempty" json:"-"`
	ProbeInFlight bool            `bson:"probeInFlight,omitempty" json:"-"`
	Samples       []BreakerSample `bson:"samples,omitempty" json:"-"`
}

// BreakerSample is one recent attempt result.
type BreakerSample struct {
	At      time.Time `bson:"at"`
	Success bool      `bson:"success"`
}

// EndpointCreate is POST /v1/webhook-endpoints.
type EndpointCreate struct {
	Name       string          `json:"name" doc:"Display name, unique in the organisation" example:"CRM inbound"`
	Kind       string          `json:"kind" enum:"http,nats,kafka,sqs,sns,pubsub,integration" doc:"http or a sink: nats, kafka, sqs, sns, pubsub, integration (each sink needs its switch). Required by the schema" example:"http"`
	Enabled    *bool           `json:"enabled,omitempty" doc:"Start enabled (default true)" example:"true"`
	URL        string          `json:"url,omitempty" doc:"HTTPS URL for kind http; checked against the egress policy (private addresses are refused unless allowed)" example:"https://crm.example.com/hooks/emailbridge"`
	SinkConfig map[string]any  `json:"sinkConfig,omitempty" doc:"Sink settings for kinds other than http (e.g. subject for nats, topic for kafka)"`
	Events     []string        `json:"events" doc:"Event types to receive; * = all"`
	Filter     EndpointFilter  `json:"filter,omitempty" doc:"Account and folder filter"`
	Headers    []WebhookHeader `json:"headers,omitempty" doc:"Custom request headers"`
	Payload    PayloadOptions  `json:"payload,omitempty" doc:"Optional data to include"`
	Secret     string          `json:"secret,omitempty" doc:"Signing secret (whsec_ + 32 base64 bytes); generated when omitted and returned once by rotate-secret" example:"whsec_6Q2rT9vK1mX8pL4nB7cZ3dF0hJ5sA2wE1yR4uI8oP0s="`
	Security   SecurityWrite   `json:"security,omitempty" doc:"Signature mode and mTLS"`
	Retry      RetryOptions    `json:"retry,omitempty" doc:"Retry policy; when sent, all four fields are required (0 or an empty string takes the default)"`
	Delivery   *DeliveryUpdate `json:"delivery,omitempty" doc:"Partial: omitted fields take the defaults"`
	Switches   map[string]any  `json:"switches,omitempty" doc:"Per-endpoint feature switches"`
	Rule       *EndpointRule   `json:"rule,omitempty" doc:"Optional CEL filter / map (rule mode)"`
}

// SecurityWrite is create/update security including optional tls PEMs.
type SecurityWrite struct {
	SignatureMode string    `json:"signatureMode,omitempty" enum:"standard,bodyOnly" doc:"standard or bodyOnly" example:"standard"`
	TLS           *TLSInput `json:"tls,omitempty" doc:"Client certificate for mTLS (needs the webhook.mtls switch); null removes it"`
}

// EndpointUpdate is PATCH /v1/webhook-endpoints/{endpointId}.
type EndpointUpdate struct {
	Name       *string          `json:"name,omitempty" doc:"New display name" example:"CRM inbound (EU)"`
	Enabled    *bool            `json:"enabled,omitempty" doc:"Enable or disable; enabling clears disabledReason" example:"true"`
	URL        *string          `json:"url,omitempty" doc:"New URL" example:"https://crm.example.com/hooks/emailbridge/v2"`
	SinkConfig *map[string]any  `json:"sinkConfig,omitempty" doc:"Replaces the sink settings"`
	Events     *[]string        `json:"events,omitempty" doc:"Replaces the event types"`
	Filter     *EndpointFilter  `json:"filter,omitempty" doc:"Replaces the filter"`
	Headers    *[]WebhookHeader `json:"headers,omitempty" doc:"Replaces the custom headers; a secret header sent without value keeps its stored value"`
	Payload    *PayloadOptions  `json:"payload,omitempty" doc:"Replaces the payload options"`
	Secret     *string          `json:"secret,omitempty" doc:"Replaces the signing secret immediately (use rotate-secret for an overlap)" example:"whsec_6Q2rT9vK1mX8pL4nB7cZ3dF0hJ5sA2wE1yR4uI8oP0s="`
	Security   *SecurityWrite   `json:"security,omitempty" doc:"Replaces the security options"`
	Retry      *RetryOptions    `json:"retry,omitempty" doc:"Replaces the retry policy"`
	Delivery   *DeliveryUpdate  `json:"delivery,omitempty" doc:"Changes the given delivery options"`
	Switches   *map[string]any  `json:"switches,omitempty" doc:"Replaces the per-endpoint switches"`
	Rule       *EndpointRule    `json:"rule,omitempty" doc:"Replaces the rule; saving a new filter/map creates a rule version"`
}

// LastDelivery is the endpoint's most recent delivery summary (list/get enrichment).
type LastDelivery struct {
	State      string    `json:"state" doc:"State of the newest delivery" example:"succeeded"`
	StatusCode int       `json:"statusCode,omitempty" doc:"HTTP status of its last attempt" example:"200"`
	At         time.Time `json:"at" doc:"When it was created" example:"2026-10-27T09:14:23Z"`
}

// EndpointView is the secret-safe API representation.
type EndpointView struct {
	ID             string          `json:"id" doc:"Endpoint ID (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	OrganizationID string          `json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string          `json:"name" doc:"Display name" example:"CRM inbound"`
	Kind           string          `json:"kind" enum:"http,nats,kafka,sqs,sns,pubsub,integration" doc:"http or a sink kind" example:"http"`
	Enabled        bool            `json:"enabled" doc:"Deliveries are made" example:"true"`
	URL            string          `json:"url,omitempty" doc:"Target URL (kind http)" example:"https://crm.example.com/hooks/emailbridge"`
	SinkConfig     map[string]any  `json:"sinkConfig,omitempty" doc:"Sink settings (secrets redacted)"`
	Events         []string        `json:"events" doc:"Subscribed event types"`
	Filter         EndpointFilter  `json:"filter,omitempty" doc:"Account and folder filter"`
	Headers        []WebhookHeader `json:"headers,omitempty" doc:"Custom request headers (secret values omitted)"`
	Payload        PayloadOptions  `json:"payload,omitempty" doc:"Optional data included"`
	SecretPreview  string          `json:"secretPreview,omitempty" doc:"First and last characters of the active signing secret" example:"whsec_…0Ps="`
	Security       SecurityView    `json:"security" doc:"Signature mode and mTLS"`
	Retry          RetryOptions    `json:"retry" doc:"Retry policy"`
	Delivery       DeliveryOptions `json:"delivery" doc:"Ordering, concurrency and content type"`
	DisabledReason *DisabledReason `json:"disabledReason,omitempty" doc:"Why the endpoint was disabled automatically"`
	Breaker        Breaker         `json:"breaker" doc:"Circuit breaker state"`
	Switches       map[string]any  `json:"switches,omitempty" doc:"Per-endpoint feature switches"`
	Rule           *EndpointRule   `json:"rule,omitempty" doc:"CEL rule (rule mode)"`
	LastDelivery   *LastDelivery   `json:"lastDelivery,omitempty" doc:"Newest delivery"`
	CreatedAt      time.Time       `json:"createdAt" doc:"When the endpoint was created" example:"2026-10-20T10:30:00Z"`
	UpdatedAt      time.Time       `json:"updatedAt" doc:"When it last changed" example:"2026-10-26T14:02:51Z"`
	Version        int64           `json:"version" doc:"Document version; the ETag" example:"5"`
}

// EndpointList is GET /v1/webhook-endpoints.
type EndpointList struct {
	Data        []EndpointView `json:"data" doc:"The endpoints of this page."`
	NextCursor  *string        `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6IndoZV8wMW01Y3NoeWMwZDIzeGtocmJ6N2U0OTBwbSJ9"`
	Total       *int64         `json:"total,omitempty" doc:"Total number of matching endpoints (only with page and includeTotal)." example:"2"`
	TotalCapped *bool          `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

// RotateSecretResult returns the new secret exactly once.
type RotateSecretResult struct {
	Secret        string     `json:"secret" doc:"The new signing secret; shown only in this response" example:"whsec_Zt8mQ1pV7xK3nB9cL5dH2jF6sA0wE4yR8uI1oP3sT6g="`
	SecretPreview string     `json:"secretPreview" doc:"Preview of the new secret" example:"whsec_…T6g="`
	ExpiresAt     *time.Time `json:"expiresAt" doc:"When the previous secret stops signing (both sign until then; null when there was none)" example:"2026-10-28T09:40:00Z"`
	ID            string     `json:"id" doc:"ID of the new secret" example:"3f9a1c0b7e2d4a58"`
}

// Delivery is the stored delivery history record.
type Delivery struct {
	ID             string            `bson:"_id" json:"id" doc:"Delivery ID (dlv_…)" example:"dlv_01m5dvdxw0j7a55tjavm1fmhex"`
	OrganizationID string            `bson:"organizationId" json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      string            `bson:"accountId,omitempty" json:"accountId,omitempty" doc:"Account of the event (acc_…)" example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	EventID        string            `bson:"eventId" json:"eventId" doc:"Delivered event (evt_…)" example:"evt_01m5czx69032gsbkp82n4ajd1w"`
	EventType      string            `bson:"eventType" json:"eventType" doc:"Event type" example:"message.received"`
	EndpointID     string            `bson:"endpointId" json:"endpointId" doc:"Target endpoint (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	RouteID        string            `bson:"routeId,omitempty" json:"routeId,omitempty" doc:"Route that matched (rte_…); omitted for direct subscriptions" example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	State          string            `bson:"state" json:"state" enum:"pending,delivering,retrying,succeeded,failed,deadLettered,cancelled" doc:"pending, delivering, retrying, succeeded, failed, deadLettered or cancelled" example:"succeeded"`
	Attempts       []DeliveryAttempt `bson:"attempts" json:"attempts,omitempty" doc:"Attempts, oldest first"`
	LastAttemptAt  *time.Time        `bson:"lastAttemptAt,omitempty" json:"lastAttemptAt,omitempty" doc:"Time of the last attempt" example:"2026-10-27T09:14:23Z"`
	NextAttemptAt  *time.Time        `bson:"nextAttemptAt,omitempty" json:"nextAttemptAt,omitempty" doc:"When the next retry is due (retrying)" example:"2026-10-27T09:14:33Z"`
	ReplayOf       string            `bson:"replayOf,omitempty" json:"replayOf,omitempty" doc:"Original delivery when this one is a replay (dlv_…)" example:"dlv_01m5dsa5x0v78ag81yce7hjpr3"`
	ReplayedAt     *time.Time        `bson:"replayedAt,omitempty" json:"replayedAt,omitempty" doc:"When this delivery was replayed" example:"2026-10-27T10:00:00Z"`
	IsReplay       bool              `bson:"isReplay" json:"-"`
	// MappedBody is the legacy plaintext mapped body (rows written before 2026-10-27); read-only now.
	MappedBody string `bson:"mappedBody,omitempty" json:"-"`
	// MappedBodyEnc is the rule-mapped request body, encrypted (it can hold settings.env secrets).
	MappedBodyEnc crypto.EncryptedString `bson:"mappedBodyEnc,omitempty" json:"-"`
	CreatedAt     time.Time              `bson:"createdAt" json:"createdAt" doc:"When the delivery was created" example:"2026-10-27T09:14:22Z"`
	UpdatedAt     time.Time              `bson:"updatedAt" json:"-"`
	ExpireAt      time.Time              `bson:"expireAt" json:"expireAt" doc:"When the delivery record expires (delivery history retention)" example:"2026-11-26T09:14:22Z"`
	Version       int64                  `bson:"version" json:"-"`
}

// DeliveryAttempt is one bounded response summary.
type DeliveryAttempt struct {
	At              time.Time         `bson:"at" json:"at" doc:"When the attempt started" example:"2026-10-27T09:14:23Z"`
	StatusCode      int               `bson:"statusCode,omitempty" json:"statusCode,omitempty" doc:"HTTP status of the answer" example:"200"`
	LatencyMs       int64             `bson:"latencyMs" json:"latencyMs" doc:"Time to the answer in milliseconds" example:"184"`
	Kind            string            `bson:"kind,omitempty" json:"kind,omitempty" doc:"Error kind of a failed attempt: temporary, permanent, rate_limited or internal; omitted on success" example:"temporary"`
	Error           string            `bson:"error,omitempty" json:"error,omitempty" doc:"Problem code or network error of a failed attempt" example:"webhook_timeout"`
	ResponseExcerpt string            `bson:"responseExcerpt,omitempty" json:"responseExcerpt,omitempty" doc:"First bytes of the response body (at most 4 KiB by default)" example:"{\"ok\":true}"`
	ResponseHeaders map[string]string `bson:"responseHeaders,omitempty" json:"responseHeaders,omitempty" doc:"Retry-After, Content-Type and X-Request-Id of the answer, when present"`
}

// DeliveryFilter is GET /v1/deliveries.
type DeliveryFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Q            string
	EndpointID   string
	EndpointIDs  []string // P3.4a granular allow-list (when set, overrides empty EndpointID)
	RouteID      string
	EventID      string
	EventType    string
	AccountID    string
	State        string // single state, or comma-separated for $in (e.g. failed,deadLettered)
	From         time.Time
	To           time.Time
}

// DeliveryList is GET /v1/deliveries.
type DeliveryList struct {
	Data        []Delivery `json:"data" doc:"The deliveries of this page."`
	NextCursor  *string    `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6ImRsdl8wMW01ZHZkeHcwajdhNTV0amF2bTFmbWhleCJ9"`
	Total       *int64     `json:"total,omitempty" doc:"Total number of matching deliveries (only with page and includeTotal)." example:"2"`
	TotalCapped *bool      `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}
