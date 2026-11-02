// Package queues is the W72 queue page: it lists the domain records behind the JetStream work queues
// (outbound messages, webhook deliveries, system mail) and cancels them — one by one, by id, by filter or the
// whole queue — always through the record (moving it to its cancelled state), never by purging the stream.
// The worker that later pulls a cancelled record's stream entry acks it without work.
package queues

import (
	"time"

	"github.com/radiobros/emailbridge/internal/jobs"
)

// Queue names are the JetStream stream names (S4 §1), as in GET / PATCH /v1/queues.
const (
	QueueSending = "EMAILBRIDGE_SUBMIT"
	QueueWebhook = "EMAILBRIDGE_DELIVERY"
	QueueSysmail = "EMAILBRIDGE_SYSMAIL"
	QueueEvents  = "EMAILBRIDGE_EVENTS"
)

// Queue kinds (GET /v1/queues `kind`).
const (
	KindSending    = "sending"
	KindWebhooks   = "webhooks"
	KindSystemMail = "systemMail"
	KindEvents     = "events"
)

// Item kinds.
const (
	ItemOutbound = "outboundMessage"
	ItemDelivery = "webhookDelivery"
	ItemSysmail  = "systemMail"
)

// Scopes.
const (
	ScopeOrganization = "organization"
	ScopeAll          = "all"
)

// Skip reasons of a bulk cancel.
const (
	ReasonInFlight = "inFlight"
	ReasonFinal    = "final"
	ReasonNotFound = "notFound"
)

// Limits.
const (
	MaxCancelIDs  = 500
	maxListLimit  = 100
	defListLimit  = 25
	totalCap      = 10_000
	maxSearchLen  = 200
	cancelBatch   = 500
	jobKindCancel = "queues.cancel"
	topicUpdates  = "queues.updates"
	typeChanged   = "queue.changed"
)

// Item is one record waiting in (or being processed from) a queue.
type Item struct {
	ID                   string     `json:"id" doc:"Record ID: an outbound message (out_…), a webhook delivery (dlv_…) or a system mail (job_…)." example:"out_01m5f2c8k0q7d3w9x4b6n1t5hz"`
	Queue                string     `json:"queue" doc:"Queue (JetStream stream name) the record waits in." example:"EMAILBRIDGE_SUBMIT"`
	Kind                 string     `json:"kind" enum:"outboundMessage,webhookDelivery,systemMail" doc:"Kind of record." example:"outboundMessage"`
	OrganizationID       string     `json:"organizationId,omitempty" doc:"Organisation of the record; absent for system mail." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	State                string     `json:"state" enum:"scheduled,queued,retrying,sending,pending,deadLettered,delivering" doc:"Queue state: scheduled / queued / retrying / sending (Sending); pending / retrying / deadLettered / delivering (webhook deliveries); pending / retrying / sending (system mail). retrying is a queued or pending record with at least one failed attempt." example:"retrying"`
	Cancellable          bool       `json:"cancellable" doc:"Whether the record can be cancelled now." example:"true"`
	NotCancellableReason string     `json:"notCancellableReason,omitempty" enum:"inFlight" doc:"Why it cannot be cancelled: inFlight = a worker is transmitting it right now." example:"inFlight"`
	AccountID            string     `json:"accountId,omitempty" doc:"Mail account (outbound messages, webhook deliveries of account events)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	EndpointID           string     `json:"endpointId,omitempty" doc:"Webhook endpoint (webhook deliveries)." example:"whe_01m5d9x2r0f4y8j6k3c1v7q2pn"`
	EventType            string     `json:"eventType,omitempty" doc:"Event type delivered (webhook deliveries)." example:"message.received"`
	Recipient            string     `json:"recipient,omitempty" doc:"First visible recipient (To, then Cc; never Bcc) of an outbound message, or the recipient of a system mail." example:"anna.rossi@example.com"`
	RecipientCount       int        `json:"recipientCount,omitempty" doc:"Visible recipients (To + Cc) of an outbound message." example:"2"`
	Subject              string     `json:"subject,omitempty" doc:"Subject of an outbound message." example:"October invoice"`
	MailKind             string     `json:"mailKind,omitempty" doc:"System mail kind (mfa_code, invitation, password_reset, email_verification, alert)." example:"invitation"`
	Attempts             int        `json:"attempts" doc:"Attempts made so far." example:"2"`
	NextAttemptAt        *time.Time `json:"nextAttemptAt,omitempty" doc:"When the next attempt is due." example:"2026-10-27T10:20:00Z"`
	SendAt               *time.Time `json:"sendAt,omitempty" doc:"Scheduled send time of an outbound message." example:"2026-10-28T08:00:00Z"`
	CreatedAt            time.Time  `json:"createdAt" doc:"When the record was queued." example:"2026-10-27T09:58:12Z"`
}

// Page is GET /v1/queues/{queue}/items.
type Page struct {
	Data        []Item `json:"data" doc:"Items of the page."`
	Total       int64  `json:"total" doc:"Items matching the query (capped at 10 000)." example:"128"`
	TotalCapped bool   `json:"totalCapped" doc:"True when total hit the 10 000 cap." example:"false"`
	Page        int    `json:"page" doc:"1-based page number." example:"1"`
	Limit       int    `json:"limit" doc:"Page size." example:"25"`
}

// Counts are the record counts of one queue for a scope (GET /v1/queues `items`).
type Counts struct {
	Waiting      int64      `json:"waiting" doc:"Ready and waiting for a worker (queued / pending without a failed attempt)." example:"42"`
	Scheduled    int64      `json:"scheduled" doc:"Outbound messages scheduled for later (send time, send window or hold)." example:"7"`
	Retrying     int64      `json:"retrying" doc:"Waiting for a retry after a failed attempt." example:"3"`
	InFlight     int64      `json:"inFlight" doc:"Being transmitted right now (not cancellable)." example:"1"`
	DeadLettered int64      `json:"deadLettered" doc:"Webhook deliveries that exhausted their retries." example:"2"`
	OldestAt     *time.Time `json:"oldestAt,omitempty" doc:"createdAt of the oldest cancellable record of the scope; absent when there is none." example:"2026-10-27T08:41:05Z"`
}

// Filter selects records for a bulk cancel (and is the list query without paging).
type Filter struct {
	State       string     `json:"state,omitempty" doc:"One queue state (see Item.state)." example:"retrying"`
	AccountID   string     `json:"accountId,omitempty" doc:"Only this account (Sending, webhook deliveries)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	EndpointID  string     `json:"endpointId,omitempty" doc:"Only this webhook endpoint (webhook deliveries)." example:"whe_01m5d9x2r0f4y8j6k3c1v7q2pn"`
	CreatedFrom *time.Time `json:"createdFrom,omitempty" doc:"Queued at or after (RFC 3339)." example:"2026-10-27T00:00:00Z"`
	CreatedTo   *time.Time `json:"createdTo,omitempty" doc:"Queued before (RFC 3339, exclusive)." example:"2026-10-27T12:00:00Z"`
	Q           string     `json:"q,omitempty" maxLength:"200" doc:"Case-insensitive prefix of the recipient or subject (Sending), event type (webhook deliveries) or recipient (system mail)." example:"anna"`
}

func (f Filter) empty() bool {
	return f.State == "" && f.AccountID == "" && f.EndpointID == "" && f.CreatedFrom == nil && f.CreatedTo == nil && f.Q == ""
}

// CancelRequest is POST /v1/queues/{queue}/items/cancel.
type CancelRequest struct {
	IDs     []string `json:"ids,omitempty" maxItems:"500" doc:"Cancel these records (1–500), synchronously." example:"[\"out_01m5f2c8k0q7d3w9x4b6n1t5hz\"]"`
	Filter  *Filter  `json:"filter,omitempty" doc:"Cancel every cancellable record matching this filter (at least one criterion), as a job."`
	All     bool     `json:"all,omitempty" doc:"Cancel the whole queue in the scope, as a job; requires confirm." example:"false"`
	Scope   string   `json:"scope,omitempty" enum:"organization,all" doc:"organization (default) or all organisations (instance owner)." example:"organization"`
	Confirm string   `json:"confirm,omitempty" maxLength:"64" doc:"With all: the queue name, typed by the user to confirm." example:"EMAILBRIDGE_SUBMIT"`
}

// Skipped is a record a cancel by ids did not cancel.
type Skipped struct {
	ID     string `json:"id" doc:"Record ID." example:"out_01m5f2d1a0h6s2k8m4p9r3v7wx"`
	Reason string `json:"reason" enum:"inFlight,final,notFound" doc:"inFlight: being transmitted; final: already sent, failed or cancelled; notFound: unknown in the scope." example:"inFlight"`
}

// CancelResult is the answer of a cancel: the synchronous counts (ids) or the job (filter / all).
type CancelResult struct {
	Job       *jobs.Job `json:"job,omitempty" doc:"The queues.cancel job (filter / all); poll GET /v1/jobs/{jobId}."`
	Matched   *int64    `json:"matched,omitempty" doc:"Cancellable records matching the filter when the request was made (filter / all)." example:"318"`
	Cancelled *int64    `json:"cancelled,omitempty" doc:"Records cancelled (ids; 0 for an empty filter match)." example:"2"`
	Skipped   []Skipped `json:"skipped,omitempty" doc:"Records not cancelled, with the reason (ids)."`
}

// JobResult is the result of a queues.cancel job.
type JobResult struct {
	Matched   int64 `json:"matched"`
	Cancelled int64 `json:"cancelled"`
	Skipped   int64 `json:"skipped"`
}

// Change is the data of a queue.changed realtime message.
type Change struct {
	Queue     string `json:"queue"`
	Cancelled int64  `json:"cancelled"`
	JobID     string `json:"jobId,omitempty"`
	Done      int64  `json:"done,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Finished  bool   `json:"finished,omitempty"`
}
