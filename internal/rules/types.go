package rules

import (
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collection names (S1 §5).
const (
	RoutesCollection        = "routes"
	RouteVersionsCollection = "routeVersions"
	RuleErrorsCollection    = "ruleErrors"
	EndpointsCollection     = "webhookEndpoints"
)

// RouteLastError is the route's most recent ruleErrors summary (list/get enrichment).
type RouteLastError struct {
	Code    string    `json:"code" doc:"Rule error phase as a code: compile, filter or map" example:"map"`
	Message string    `json:"message" doc:"Evaluation error text (redacted)" example:"no such key: subject"`
	At      time.Time `json:"at" doc:"When it happened" example:"2026-10-27T09:14:22Z"`
}

// Route is S1 `routes`.
type Route struct {
	ID             string                 `bson:"_id" json:"id" doc:"Route ID (rte_…)" example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	OrganizationID string                 `bson:"organizationId" json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string                 `bson:"name" json:"name" doc:"Display name" example:"Invoices to accounting"`
	Enabled        bool                   `bson:"enabled" json:"enabled" doc:"Disabled routes are skipped by the router" example:"true"`
	EndpointID     string                 `bson:"endpointId" json:"endpointId" doc:"Webhook endpoint that receives the matching events (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	CurrentVersion int                    `bson:"currentVersion" json:"currentVersion" doc:"Number of the active filter/map version (see …/versions)" example:"3"`
	Filter         string                 `bson:"filter" json:"filter" doc:"CEL boolean expression over the event (variables event, type, data, account, env, now; helpers such as fromDomain(), subjectContains(), inList()). Empty or true matches every event" example:"type == \"message.received\" && isInbox() && hasAttachments() && fromDomain() in [\"example.org\", \"example.net\"]"`
	Map            string                 `bson:"map,omitempty" json:"map,omitempty" doc:"Optional CEL expression whose result replaces the delivered body (the envelope when empty)" example:"{\"messageId\": data.messageId, \"from\": from(), \"subject\": subject()}"`
	Builder        map[string]any         `bson:"builder,omitempty" json:"builder,omitempty" doc:"No-code builder model the filter was compiled from (kept so the editor can show it again)"`
	Secret         crypto.EncryptedString `bson:"secret,omitempty" json:"-"`
	Headers        map[string]any         `bson:"headers,omitempty" json:"headers,omitempty" doc:"Extra request headers for deliveries of this route (name to value)"`
	Payload        map[string]any         `bson:"payload,omitempty" json:"payload,omitempty" doc:"Payload options for this route's deliveries (same keys as the endpoint's payload)"`
	Order          int                    `bson:"order" json:"order" doc:"Evaluation order, ascending; ties by ID" example:"10"`
	Switches       map[string]any         `bson:"switches,omitempty" json:"switches,omitempty" doc:"Per-route feature switches"`
	LastError      *RouteLastError        `bson:"-" json:"lastError,omitempty" doc:"Most recent rule error; omitted when there is none"`
	CreatedAt      time.Time              `bson:"createdAt" json:"createdAt" doc:"When the route was created" example:"2026-10-21T08:00:00Z"`
	UpdatedAt      time.Time              `bson:"updatedAt" json:"updatedAt" doc:"When the route last changed" example:"2026-10-26T14:20:05Z"`
	Version        int64                  `bson:"version" json:"version" doc:"Document version; the ETag" example:"4"`
}

// RouteVersion is S1 `routeVersions`.
type RouteVersion struct {
	ID             string         `bson:"_id" json:"id" doc:"Route version ID (rtv_…)" example:"rtv_01m5dckp30mdexhj4qp140dym4"`
	OrganizationID string         `bson:"organizationId" json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	RouteID        string         `bson:"routeId" json:"routeId" doc:"Route ID (rte_…)" example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Version        int            `bson:"version" json:"version" doc:"Version number, from 1" example:"3"`
	Filter         string         `bson:"filter" json:"filter" doc:"Filter of this version" example:"type == \"message.received\" && hasAttachments()"`
	Map            string         `bson:"map,omitempty" json:"map,omitempty" doc:"Map of this version" example:"{\"messageId\": data.messageId}"`
	Builder        map[string]any `bson:"builder,omitempty" json:"builder,omitempty" doc:"Builder model of this version"`
	CreatedBy      string         `bson:"createdBy" json:"createdBy" doc:"User or API key that saved it" example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt      time.Time      `bson:"createdAt" json:"createdAt" doc:"When it was saved" example:"2026-10-26T14:20:05Z"`
}

// RuleError is S1 `ruleErrors`.
type RuleError struct {
	ID             string    `bson:"_id,omitempty" json:"id,omitempty"`
	OrganizationID string    `bson:"organizationId" json:"organizationId"`
	RouteID        string    `bson:"routeId" json:"routeId"`
	Version        int       `bson:"version" json:"version"`
	At             time.Time `bson:"at" json:"at"`
	Phase          string    `bson:"phase" json:"phase"` // compile | filter | map
	Message        string    `bson:"message" json:"message"`
	EventID        string    `bson:"eventId,omitempty" json:"eventId,omitempty"`
	PayloadExcerpt string    `bson:"payloadExcerpt,omitempty" json:"payloadExcerpt,omitempty"`
	ExpireAt       time.Time `bson:"expireAt" json:"expireAt"`
}

// RouteCreate is the create body.
type RouteCreate struct {
	Name       string         `json:"name" doc:"Display name" example:"Invoices to accounting"`
	Enabled    *bool          `json:"enabled,omitempty" doc:"Start enabled (default true)" example:"true"`
	EndpointID string         `json:"endpointId" doc:"Target webhook endpoint (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Filter     string         `json:"filter,omitempty" doc:"CEL filter; empty and no builder = match everything" example:"type == \"message.received\" && hasAttachments()"`
	Map        string         `json:"map,omitempty" doc:"Optional CEL map of the delivered body" example:"{\"messageId\": data.messageId, \"subject\": subject()}"`
	Builder    map[string]any `json:"builder,omitempty" doc:"No-code builder model; compiled to the filter when filter is empty"`
	Headers    map[string]any `json:"headers,omitempty" doc:"Extra request headers (name to value)"`
	Payload    map[string]any `json:"payload,omitempty" doc:"Payload options for this route's deliveries"`
	Order      int            `json:"order,omitempty" doc:"Evaluation order, ascending" example:"10"`
	Secret     string         `json:"secret,omitempty" doc:"Signing secret for this route's deliveries (default: the endpoint's); stored encrypted, never returned" example:"whsec_6Q2rT9vK1mX8pL4nB7cZ3dF0hJ5sA2wE"`
}

// RouteUpdate is the merge-patch body.
type RouteUpdate struct {
	Name       *string         `json:"name,omitempty" doc:"New display name" example:"Invoices and receipts to accounting"`
	Enabled    *bool           `json:"enabled,omitempty" doc:"Enable or disable" example:"false"`
	EndpointID *string         `json:"endpointId,omitempty" doc:"New target endpoint (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Filter     *string         `json:"filter,omitempty" doc:"New filter; saving a new filter/map/builder creates a new version" example:"type == \"message.received\" && hasAttachment(\"pdf\")"`
	Map        *string         `json:"map,omitempty" doc:"New map; empty string removes it" example:"{\"messageId\": data.messageId}"`
	Builder    *map[string]any `json:"builder,omitempty" doc:"New builder model"`
	Headers    *map[string]any `json:"headers,omitempty" doc:"Replaces the extra request headers"`
	Payload    *map[string]any `json:"payload,omitempty" doc:"Replaces the payload options"`
	Order      *int            `json:"order,omitempty" doc:"New evaluation order" example:"20"`
	Secret     *string         `json:"secret,omitempty" doc:"New signing secret; empty string falls back to the endpoint's" example:"whsec_6Q2rT9vK1mX8pL4nB7cZ3dF0hJ5sA2wE"`
}

// RouteTestRequest is POST …/test body (W73: event | eventId | sample).
//
// W73b: an optional draft (`filter` / `map` / `builder`) is evaluated instead of the stored rule, so the admin
// editor can show MATCH / NO MATCH and the mapped output while the user edits. A draft is compiled uncached (it
// never touches the router's program cache) and needs no stored rule.
type RouteTestRequest struct {
	Event   map[string]any `json:"event,omitempty" doc:"A CloudEvents envelope to evaluate (one of event, eventId, sample)"`
	EventID string         `json:"eventId,omitempty" doc:"A stored event to evaluate (evt_…)" example:"evt_01m5czx69032gsbkp82n4ajd1w"`
	Sample  string         `json:"sample,omitempty" doc:"Built-in sample of this event type, e.g. message.received" example:"message.received"`
	Filter  *string        `json:"filter,omitempty" doc:"Draft filter evaluated instead of the stored one (W73b)."`
	Map     *string        `json:"map,omitempty" doc:"Draft map evaluated instead of the stored one (W73b)."`
	Builder *Builder       `json:"builder,omitempty" doc:"Draft builder, compiled when the draft filter is empty (W73b)."`
}

// IsDraft reports whether the request carries a draft rule.
func (r RouteTestRequest) IsDraft() bool {
	return r.Filter != nil || r.Map != nil || r.Builder != nil
}

// RouteTestResult is POST …/test response (W73 adds trace; cost alias).
type RouteTestResult struct {
	Matched    bool        `json:"matched" doc:"The filter matched" example:"true"`
	Mapped     any         `json:"mapped,omitempty" doc:"Result of the map expression (only when matched and a map is set)"`
	CostUsed   uint64      `json:"costUsed" doc:"CEL cost units used" example:"38"`
	Cost       uint64      `json:"cost" doc:"Alias of costUsed" example:"38"`
	DurationMs int64       `json:"durationMs" doc:"Evaluation time in milliseconds" example:"1"`
	Error      string      `json:"error,omitempty" doc:"Compile or evaluation error" example:"ERROR: <input>:1:9: undeclared reference to 'subjct'"`
	Trace      []TraceTerm `json:"trace,omitempty" doc:"Result of each top-level && / || term of the filter"`
	// ErrorAt locates a compile error (W73b: inline errors in the code editor).
	ErrorAt *ErrorPosition `json:"errorAt,omitempty" doc:"Where a compile error sits"`
}

// ErrorPosition is where a compile error sits: which expression and the 1-based line / column.
type ErrorPosition struct {
	Path   string `json:"path" doc:"filter or map" example:"filter"`
	Line   int    `json:"line" doc:"1-based line" example:"1"`
	Column int    `json:"column" doc:"1-based column" example:"9"`
}

// ListResult is a cursor/page list response.
type ListResult struct {
	Data        []Route `json:"data" doc:"The routes of this page."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6InJ0ZV8wMW01ZGFmeTQwcWhzbTNiMnF4MGVmNnNraCJ9"`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching routes (only with page and includeTotal)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}
