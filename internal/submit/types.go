// Package submit owns the outbound side of EmailBridge (S6): the submit API, the `outboundMessages`
// state machine, the JetStream work queue, the SMTP workers with their connection pool, the
// classification and retry policies (internal/submit/policy) and the copy of sent mail to the Sent
// folder. The sandbox gate (S6 §7) is a hook here and implemented in P1.14.
package submit

import (
	"context"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/pecreceipts"
)

// Collections (S1 §4).
const (
	// Collection holds one document per submitted message.
	Collection = "outboundMessages"
	// IdempotencyCollection holds the replayable responses of POST requests (S1 §4, S2 §1).
	IdempotencyCollection = "idempotencyKeys"
)

// States of an outbound message (S6 §1).
const (
	StateScheduled      = "scheduled"
	StateQueued         = "queued"
	StateSending        = "sending"
	StateSent           = "sent"
	StateFailed         = "failed"
	StateOutcomeUnknown = "outcomeUnknown"
	StateCancelled      = "cancelled"
	StateCaptured       = "captured"
)

// FinalStates start the retention clocks (S6 §1).
var FinalStates = []string{StateSent, StateFailed, StateOutcomeUnknown, StateCancelled, StateCaptured}

// Copy-to-Sent modes and result states (S1, S6 §5).
const (
	CopyAuto   = "auto"
	CopyAlways = "always"
	CopyNever  = "never"

	CopyPending = "pending"
	CopyDone    = "done"
	CopySkipped = "skipped"
	CopyFailed  = "failed"
)

// CopyMaxAttempts is the number of appendSent attempts before copyToSent.state becomes failed (S6 §5).
const CopyMaxAttempts = 3

// ResponseMax caps the stored server reply of an attempt (S1 `outboundMessages.attempts`).
const ResponseMax = 1 << 10

// Envelope is the stored SMTP envelope. Bcc addresses live only inside the raw message, so only their
// number is kept (S1 §4, S11).
type Envelope struct {
	From     string   `bson:"from" json:"from" doc:"MAIL FROM address." example:"billing@example.com"`
	To       []string `bson:"to,omitempty" json:"to,omitempty" doc:"RCPT TO addresses from To (or the raw envelope)." example:"[\"anna.rossi@example.org\"]"`
	Cc       []string `bson:"cc,omitempty" json:"cc,omitempty" doc:"RCPT TO addresses from Cc." example:"[\"accounts@example.org\"]"`
	BccCount int      `bson:"bccCount,omitempty" json:"bccCount,omitempty" doc:"Number of blind-copy recipients (their addresses are never returned)." example:"1"`
}

// Recipients returns every envelope recipient in RCPT TO order (to, cc, then bcc from the raw message).
func (e Envelope) Recipients(bcc []string) []string {
	out := make([]string, 0, len(e.To)+len(e.Cc)+len(bcc))
	out = append(out, e.To...)
	out = append(out, e.Cc...)
	return append(out, bcc...)
}

// Attempt is one SMTP attempt (S1 §4). The response is redacted and capped at ResponseMax.
type Attempt struct {
	At        time.Time `bson:"at" json:"at" doc:"When the attempt started." example:"2026-10-27T10:20:03Z"`
	Stage     string    `bson:"stage" json:"stage" doc:"Last SMTP stage reached: connect, ehlo, auth, mailFrom, rcptTo, data or dataEnd." example:"dataEnd"`
	Code      int       `bson:"code,omitempty" json:"code,omitempty" doc:"SMTP reply code of the stage." example:"250"`
	Enhanced  string    `bson:"enhanced,omitempty" json:"enhanced,omitempty" doc:"RFC 3463 enhanced status code." example:"2.0.0"`
	Response  string    `bson:"response,omitempty" json:"response,omitempty" doc:"Server reply, redacted and cut at 1 KiB." example:"250 2.0.0 Ok: queued as 4XkLm72Qz1"`
	Kind      string    `bson:"kind,omitempty" json:"kind,omitempty" doc:"Classification: success, temporary, permanent, rate_limited, auth, outcome_unknown …" example:"success"`
	LatencyMs int64     `bson:"latencyMs,omitempty" json:"latencyMs,omitempty" doc:"Duration of the attempt in milliseconds." example:"1430"`
	Rejected  []Reject  `bson:"rejected,omitempty" json:"rejected,omitempty" doc:"Recipients refused permanently while others were accepted."`
	Rewritten bool      `bson:"rewritten,omitempty" json:"rewritten,omitempty" doc:"true when the sandbox rewrote the recipients of this attempt." example:"false"`
}

// Reject is a recipient refused permanently while others were accepted (S6 §3.4).
type Reject struct {
	Address  string `bson:"address" json:"address" doc:"Refused recipient." example:"unknown.user@example.net"`
	Code     int    `bson:"code" json:"code" doc:"SMTP reply code." example:"550"`
	Enhanced string `bson:"enhanced,omitempty" json:"enhanced,omitempty" doc:"Enhanced status code." example:"5.1.1"`
	Message  string `bson:"message" json:"message" doc:"Server reply text." example:"Recipient address rejected: User unknown"`
}

// CopyToSent is the state of the APPEND to the Sent folder (S6 §5).
type CopyToSent struct {
	Enabled  bool   `bson:"enabled" json:"enabled" doc:"Whether a copy is appended to the Sent folder after sending." example:"true"`
	Path     string `bson:"path,omitempty" json:"path,omitempty" doc:"Sent folder path." example:"Sent"`
	State    string `bson:"state" json:"state" doc:"pending, done, skipped or failed." example:"done"`
	Attempts int    `bson:"attempts,omitempty" json:"-"`
	Code     string `bson:"code,omitempty" json:"code,omitempty" doc:"Problem code of a failed copy." example:"mail_auth_failed"`
}

// Message is the stored `outboundMessages` document (S1 §4).
type Message struct {
	ID              string     `bson:"_id"`
	OrganizationID  string     `bson:"organizationId"`
	AccountID       string     `bson:"accountId"`
	State           string     `bson:"state"`
	Policy          string     `bson:"policy"`
	MessageIDHeader string     `bson:"messageIdHeader"`
	Envelope        Envelope   `bson:"envelope"`
	Bcc             []string   `bson:"bcc,omitempty"` // envelope-only recipients, never in the headers
	Subject         string     `bson:"subject,omitempty"`
	RawRef          string     `bson:"rawRef,omitempty"`
	Size            int64      `bson:"size"`
	SendAt          *time.Time `bson:"sendAt,omitempty"`
	NextAttemptAt   *time.Time `bson:"nextAttemptAt,omitempty"`
	Attempts        []Attempt  `bson:"attempts,omitempty"`
	MaxAttempts     int        `bson:"maxAttempts"`
	BackoffBase     string     `bson:"backoffBase,omitempty"`
	IdempotencyKey  string     `bson:"idempotencyKey,omitempty"`
	CopyToSent      CopyToSent `bson:"copyToSent"`
	Worker          string     `bson:"worker,omitempty"`
	HeartbeatAt     *time.Time `bson:"heartbeatAt,omitempty"`
	CreatedBy       string     `bson:"createdBy,omitempty"`
	// System marks system mail (login codes, reset links, invitations): never copied to Sent, never
	// captured with its body (S10 §7).
	System bool `bson:"system,omitempty"`
	// TraceParent is the W3C trace context of the request that queued the message: the SMTP send and its
	// events continue that trace (S12 §10).
	TraceParent    string        `bson:"traceparent,omitempty"`
	CreatedAt      time.Time     `bson:"createdAt"`
	SentAt         *time.Time    `bson:"sentAt,omitempty"`
	FinalError     *errs.Problem `bson:"-"`
	FinalErrorJSON string        `bson:"finalError,omitempty"`
	Version        int64         `bson:"version"`
	// GatewayID is the optional SMTP gateway used for this message (P5.3).
	GatewayID string `bson:"gatewayId,omitempty"`
	// DSN is RFC 3461 options requested at submit (P5.3).
	DSN *DSNRequest `bson:"dsn,omitempty"`
	// ScheduleReason / ScheduleDetail explain why a message is scheduled (P5.5 send window / hold).
	ScheduleReason string `bson:"scheduleReason,omitempty"`
	ScheduleDetail string `bson:"scheduleDetail,omitempty"`
	// PEC is the linked PEC receipt status when any receipt has been applied (S17 §5.2 / W40).
	PEC *pecreceipts.OutboundPECStatus `bson:"pec,omitempty"`
	// ListID is the RFC 2919 List-Id token for this outbound (W53).
	ListID string `bson:"listId,omitempty" json:"listId,omitempty"`
}

// DSNRequest is submit/store form of RFC 3461 options (P5.3).
type DSNRequest struct {
	Notify []string `bson:"notify,omitempty" json:"notify,omitempty" doc:"RFC 3461 NOTIFY: never, or any of success, failure, delay (case-insensitive)." example:"[\"failure\",\"delay\"]"`
	Ret    string   `bson:"ret,omitempty" json:"ret,omitempty" doc:"RET: full or hdrs (what a bounce returns)." example:"hdrs"`
	EnvID  string   `bson:"envid,omitempty" json:"envid,omitempty" doc:"ENVID echoed in delivery status notifications (≤ 100 characters)." example:"inv-2026-1043"`
}

// LastAttempt returns the attempt the worker is on, or nil when there is none.
func (m *Message) LastAttempt() *Attempt {
	if len(m.Attempts) == 0 {
		return nil
	}
	return &m.Attempts[len(m.Attempts)-1]
}

// AddressInput is an address in a submit request (OpenAPI Address).
type AddressInput struct {
	Name    string `json:"name,omitempty" doc:"Display name." example:"Anna Rossi"`
	Address string `json:"address" doc:"Email address (required)." example:"anna.rossi@example.org"`
}

// AttachmentInput is one attachment of a submit request (OpenAPI AttachmentInput).
type AttachmentInput struct {
	Filename    string `json:"filename" doc:"File name shown to the recipient (required)." example:"invoice-2026-1043.pdf"`
	ContentType string `json:"contentType,omitempty" doc:"MIME type (default application/octet-stream)." example:"application/pdf"`
	Content     string `json:"content" doc:"Base64 content" example:"JVBERi0xLjcKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCg=="`
	Disposition string `json:"disposition,omitempty" enum:"attachment,inline" doc:"attachment (default) or inline (referenced from the HTML by cid:)." example:"attachment"`
	ContentID   string `json:"contentId,omitempty" doc:"Content-ID for an inline part, used as cid:<contentId> in the HTML." example:"logo@example.com"`
}

// CopyToSentInput overrides the account's copy-to-Sent settings for one message.
type CopyToSentInput struct {
	Enabled *bool  `json:"enabled,omitempty" doc:"Force (true) or skip (false) the copy to the Sent folder; default the account setting." example:"false"`
	Path    string `json:"path,omitempty" doc:"Sent folder path for this message." example:"Sent"`
}

// SubmitRequest is the body of POST /v1/accounts/{accountId}/outbound-messages (OpenAPI SubmitRequest).
type SubmitRequest struct {
	From        *AddressInput     `json:"from,omitempty" doc:"Sender; default the account address and name."`
	To          []AddressInput    `json:"to,omitempty" doc:"To recipients."`
	Cc          []AddressInput    `json:"cc,omitempty" doc:"Cc recipients."`
	Bcc         []AddressInput    `json:"bcc,omitempty" doc:"Blind-copy recipients: in the envelope only, never in the headers."`
	ReplyTo     []AddressInput    `json:"replyTo,omitempty" doc:"Reply-To addresses."`
	Subject     string            `json:"subject,omitempty" doc:"Subject." example:"Invoice 2026-1043"`
	Text        string            `json:"text,omitempty" doc:"Plain-text body." example:"Hello Anna,\n\nplease find invoice 2026-1043 attached."`
	HTML        string            `json:"html,omitempty" doc:"HTML body (tracking, when on, rewrites its links and adds the open pixel)." example:"<p>Hello Anna,</p><p>please find invoice <b>2026-1043</b> attached.</p>"`
	Headers     map[string]string `json:"headers,omitempty" doc:"Extra headers (name to value); Bcc, MIME-Version, Message-ID and Content-* are refused, as are CR/LF." example:"{\"X-Order-Id\":\"ORD-58213\"}"`
	MessageID   string            `json:"messageId,omitempty" doc:"Message-ID to use, with angle brackets (default: generated on the From domain)." example:"<20261027101500.1@example.com>"`
	Attachments []AttachmentInput `json:"attachments,omitempty" doc:"Attachments with base64 content."`
	SendAt      *time.Time        `json:"sendAt,omitempty" doc:"Send at this time instead of now (state scheduled until then)." example:"2026-10-28T08:00:00Z"`
	Policy      string            `json:"policy,omitempty" enum:"standard,atMostOnce" doc:"standard (retry until maxAttempts) or atMostOnce (never resend after an unknown outcome); default the account / EB_SUBMIT_POLICY." example:"standard"`
	MaxAttempts *int              `json:"maxAttempts,omitempty" doc:"Delivery attempts before failing (default the account / EB_SUBMIT_MAX_ATTEMPTS, 10)." example:"5"`
	CopyToSent  *CopyToSentInput  `json:"copyToSent,omitempty" doc:"Override the copy to the Sent folder for this message."`
	DryRun      bool              `json:"dryRun,omitempty" doc:"Build and validate only; nothing is stored or sent (answer 200)." example:"false"`
	Raw         string            `json:"raw,omitempty" doc:"Base64 RFC 822; mutually exclusive with structured fields" example:"RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg=="`
	// Envelope overrides the SMTP envelope of a raw message (MAIL FROM / RCPT TO), as an SMTP client sends it:
	// recipients not in the To / Cc headers are blind copies. Only with raw.
	Envelope *EnvelopeInput `json:"envelope,omitempty" doc:"SMTP envelope of a raw message: recipients not in To/Cc are Bcc. Only with raw"`
	// Template renders subject/text/html from a stored Handlebars template (P5.1).
	Template *TemplateRef `json:"template,omitempty" doc:"Render subject / text / html from a stored template (fields given here are replaced)."`
	// Recipients is mail-merge: one outbound message per entry (P5.1). Mutually exclusive with top-level to/cc/bcc.
	Recipients []MergeRecipient `json:"recipients,omitempty" doc:"Mail merge: one message per entry (1–100); top-level to / cc / bcc must be empty."`
	// Reference replies to or forwards an indexed message (P5.1).
	Reference *Reference `json:"reference,omitempty" doc:"Reply to or forward an indexed message (threading headers, quoted text)."`
	// DSN requests RFC 3461 notifications when the SMTP server advertises DSN (P5.3).
	DSN *DSNRequest `json:"dsn,omitempty" doc:"RFC 3461 delivery status notification options, when the server supports DSN."`
	// GatewayID overrides the account SMTP with an organisation gateway (P5.3).
	GatewayID string `json:"gatewayId,omitempty" doc:"Send through this organisation SMTP / API gateway instead of the account's server (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
	// TrackOpens overrides tracking.opens for this message (may only turn off; W53).
	TrackOpens *bool `json:"trackOpens,omitempty" doc:"false turns open tracking off for this message (it cannot turn it on)." example:"false"`
	// TrackClicks overrides tracking.clicks for this message (may only turn off; W53).
	TrackClicks *bool `json:"trackClicks,omitempty" doc:"false turns click tracking off for this message (it cannot turn it on)." example:"false"`
	// ListID sets RFC 2919 List-Id and scopes unsubscribes/suppression (W53).
	ListID string `json:"listId,omitempty" doc:"RFC 2919 List-Id; unsubscribes are then scoped to this list (≤ 200 characters)." example:"invoices.example.com"`
}

// TemplateRef selects a stored template and its variables (P5.1).
type TemplateRef struct {
	ID        string         `json:"id" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
	Version   *int           `json:"version,omitempty" doc:"Content version (default: current)." example:"2"`
	Variables map[string]any `json:"variables,omitempty" doc:"Template variables (merged under each merge recipient's variables)." example:"{\"customer\":{\"firstName\":\"Anna\"},\"invoice\":{\"number\":\"2026-1043\"}}"`
}

// MergeRecipient is one mail-merge row (P5.1).
type MergeRecipient struct {
	To        []AddressInput `json:"to,omitempty" doc:"To recipients of this message."`
	Cc        []AddressInput `json:"cc,omitempty" doc:"Cc recipients of this message."`
	Bcc       []AddressInput `json:"bcc,omitempty" doc:"Bcc recipients of this message."`
	Variables map[string]any `json:"variables,omitempty" doc:"Template variables for this recipient (win over template.variables)." example:"{\"customer\":{\"firstName\":\"Marco\"},\"invoice\":{\"number\":\"2026-1044\"}}"`
}

// Reference threads a reply or forward against an indexed message (P5.1).
type Reference struct {
	MessageID string `json:"messageId" doc:"Indexed message of the same account (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	Action    string `json:"action" enum:"reply,replyAll,forward" doc:"reply (to the sender), replyAll (sender and other recipients) or forward (needs recipients)." example:"reply"`
	Inline    *bool  `json:"inline,omitempty" doc:"Quote the original in the body (default true); forward with false attaches it as message/rfc822." example:"true"`
}

// MaxMergeRecipients caps mail-merge size (P5.1).
const MaxMergeRecipients = 100

// MergeAccepted is the 202 body of a mail-merge submit.
type MergeAccepted struct {
	Messages []Accepted `json:"messages" doc:"One accepted message per merge recipient, in order."`
}

// MergeDryRun is the 200 body of a mail-merge dry run.
type MergeDryRun struct {
	Results []DryRunResult `json:"results" doc:"One dry-run result per merge recipient, in order."`
}

// Accepted is the 202 body of a submit (OpenAPI SubmitAccepted).
type Accepted struct {
	ID        string     `json:"id" doc:"Outbound message ID (out_…)." example:"out_01m5dsa5x0s3ggtntesyf0cb50"`
	MessageID string     `json:"messageId" doc:"Message-ID header of the built message." example:"<20261027101500.1@example.com>"`
	State     string     `json:"state" doc:"queued, or scheduled when sendAt is in the future." example:"queued"`
	SendAt    *time.Time `json:"sendAt,omitempty" doc:"When a scheduled message will be sent." example:"2026-10-28T08:00:00Z"`
}

// DryRunResult is the 200 body of a dry run (OpenAPI DryRunResult).
type DryRunResult struct {
	MessageID string   `json:"messageId" doc:"Message-ID header of the built message." example:"<20261027101500.1@example.com>"`
	Size      int64    `json:"size" doc:"Size of the built message in bytes." example:"2187"`
	Envelope  Envelope `json:"envelope" doc:"SMTP envelope the message would be sent with."`
	Raw       string   `json:"raw,omitempty" doc:"Base64 when includeRaw=true" example:"RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg=="`
}

// View is the API representation of an outbound message (OpenAPI OutboundMessage).
type View struct {
	ID              string                         `json:"id" doc:"Outbound message ID (out_…)." example:"out_01m5dsa5x0s3ggtntesyf0cb50"`
	OrganizationID  string                         `json:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID       string                         `json:"accountId" doc:"Sending account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	State           string                         `json:"state" doc:"scheduled, queued, sending, sent, failed, outcomeUnknown, cancelled or captured (sandbox)." example:"sent"`
	Policy          string                         `json:"policy" doc:"standard or atMostOnce." example:"standard"`
	MessageID       string                         `json:"messageId" doc:"Message-ID header as on the wire" example:"<20261027101500.1@example.com>"`
	MessageIDHeader string                         `json:"messageIdHeader" doc:"Message-ID header (same as messageId)." example:"<20261027101500.1@example.com>"`
	Envelope        Envelope                       `json:"envelope" doc:"SMTP envelope (Bcc only as a count)."`
	Subject         string                         `json:"subject,omitempty" doc:"Subject." example:"Invoice 2026-1043"`
	Size            int64                          `json:"size" doc:"Size of the stored message in bytes." example:"48990"`
	SendAt          *time.Time                     `json:"sendAt,omitempty" doc:"Requested send time of a scheduled message." example:"2026-10-28T08:00:00Z"`
	NextAttemptAt   *time.Time                     `json:"nextAttemptAt,omitempty" doc:"When the next attempt is due (queued messages)." example:"2026-10-27T10:20:00Z"`
	Attempts        []Attempt                      `json:"attempts,omitempty" doc:"Delivery attempts, oldest first."`
	MaxAttempts     int                            `json:"maxAttempts" doc:"Attempts before the message fails." example:"10"`
	CopyToSent      CopyToSent                     `json:"copyToSent" doc:"Copy to the Sent folder."`
	CreatedBy       string                         `json:"createdBy,omitempty" doc:"User or API key that submitted the message." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
	CreatedAt       time.Time                      `json:"createdAt" doc:"When the message was submitted." example:"2026-10-27T10:15:00Z"`
	SentAt          *time.Time                     `json:"sentAt,omitempty" doc:"When the server accepted the message." example:"2026-10-27T10:20:04Z"`
	FinalError      *errs.Problem                  `json:"finalError,omitempty" doc:"Why the message failed (RFC 9457 problem), for failed / outcomeUnknown."`
	Version         int64                          `json:"version" doc:"Optimistic-concurrency version (the ETag is \"v<version>\")." example:"6"`
	GatewayID       string                         `json:"gatewayId,omitempty" doc:"Gateway the message is sent through (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
	DSN             *DSNRequest                    `json:"dsn,omitempty" doc:"Requested delivery status notifications."`
	PEC             *pecreceipts.OutboundPECStatus `json:"pec,omitempty" doc:"PEC receipt status (certified mail accounts only)."`
}

// ViewOf redacts a stored message into its API form.
func ViewOf(m *Message) View {
	return View{
		ID: m.ID, OrganizationID: m.OrganizationID, AccountID: m.AccountID, State: m.State,
		Policy: m.Policy, MessageID: m.MessageIDHeader, MessageIDHeader: m.MessageIDHeader,
		Envelope: m.Envelope, Subject: m.Subject, Size: m.Size, SendAt: m.SendAt,
		NextAttemptAt: m.NextAttemptAt, Attempts: m.Attempts, MaxAttempts: m.MaxAttempts,
		CopyToSent: m.CopyToSent, CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt, SentAt: m.SentAt,
		FinalError: m.FinalError, Version: m.Version, GatewayID: m.GatewayID, DSN: m.DSN, PEC: m.PEC,
	}
}

// List is the body of GET /v1/outbound-messages (OpenAPI OutboundMessageList).
type List struct {
	Data        []View  `json:"data" doc:"The page of outbound messages."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6Im91dF8wMW01ZHNhNXgwczNnZ3RudGVzeWYwY2I1MCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Number of matching messages, with page and includeTotal=true (at most 10 000)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"true when total stopped at 10 000." example:"false"`
}

// ListFilter is the query of GET /v1/outbound-messages.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	AccountID    string
	AccountIDs   []string // P3.4a: set by ScopedIDs when the API key is resource-limited
	State        string
	MessageID    string
	From         string     // envelope sender address
	To           string     // envelope recipient address
	CreatedFrom  *time.Time // createdAt ≥ (inclusive)
	CreatedTo    *time.Time // createdAt < (exclusive)
}

// SMTPAccount is everything the send pipeline needs about an account: its SMTP configuration with the
// password already decrypted, the send policy, and the copy-to-Sent settings. The adapter over
// internal/accounts lives in cmd/ (it decrypts accounts.smtp.auth.pass with internal/crypto).
type SMTPAccount struct {
	ID             string
	OrganizationID string
	ExternalID     *string
	// State is the account state (S1); submit refuses `deleting` and `disabled` but allows `authError`,
	// because only IMAP authentication failed (S6 §2.1).
	State string
	// Email and Name are the default From of a message without one.
	Email string
	Name  string

	Host               string
	Port               int
	TLS                string // implicit | starttls | none
	User               string
	Password           string
	EHLO               string // accounts.network.ehloName (P5) → host of EB_PUBLIC_URL
	IgnoreCertErrors   bool
	Policy             string // standard | atMostOnce; "" = EB_SUBMIT_POLICY
	MaxAttempts        int    // 0 = EB_SUBMIT_MAX_ATTEMPTS
	BackoffBase        time.Duration
	CopyToSent         string // auto | always | never
	CopyToSentPath     string
	ProtocolLogs       bool
	ProtocolLogsRawAll bool
	// Provider is accounts.provider (imap / gmail / graph); gmail uses GmailSender (P6.2).
	Provider string
	// GatewayID when set makes PoolKey the gateway and dials its host/auth (P5.3).
	GatewayID string
	// Token, when set, returns a fresh OAuth access token and login (P6.1); the SMTP session uses XOAUTH2.
	Token func(ctx context.Context) (token, username string, err error)
	// APISend, set when the selected gateway is an HTTP API provider (P8.3: SES, Postmark, Mailgun, SendGrid,
	// Brevo), sends the message instead of an SMTP session and returns the provider's message ID.
	APISend func(ctx context.Context, mail APIMail) (providerMessageID string, err error)
	// Proxy is socks5:// or http:// CONNECT (accounts.network.proxy / gateway).
	Proxy string
	// LocalAddress binds the outbound TCP socket (accounts.network.localAddress / gateway).
	LocalAddress string
	// LocalAddresses / SMTPStrategy are the IP pool (W57); LocalAddress still wins.
	LocalAddresses []string
	SMTPStrategy   string
}

// PoolKey is the SMTP connection-pool key: gateway id when sending via a gateway, else account id (S6 §4).
func (a *SMTPAccount) PoolKey() string {
	if a != nil && a.GatewayID != "" {
		return a.GatewayID
	}
	if a == nil {
		return ""
	}
	return a.ID
}

// GatewayResolver loads decrypted gateway dial settings for the submit worker (P5.3).
type GatewayResolver interface {
	ResolveSMTP(ctx context.Context, orgID, gatewayID string) (*SMTPAccount, error)
}

// AccountSMTP resolves an account's SMTP settings. Implemented in cmd/ over internal/accounts.
type AccountSMTP interface {
	SMTP(ctx context.Context, accountID string) (*SMTPAccount, error)
}

// SentCopier appends a sent message to the account's Sent folder (S6 §5). The sync engine implements it
// through its `appendSent` command (S5 §3); without an owner it opens one short-lived IMAP connection.
type SentCopier interface {
	CopyToSent(ctx context.Context, accountID, outboundID string, raw io.Reader, date time.Time) error
}

// SandboxGate is the sandbox hook of S6 §7, implemented in P1.14. It runs after the cancellation and
// throttle gates and before the SMTP session is opened. A gate that returns handled = true has already
// written the final state and event of the message (`captured`), so the worker only acknowledges the job.
// When handled is false and rewriteTo is non-empty, every RCPT TO is replaced with rewriteTo (headers
// unchanged); the worker still sends and records Attempt.Rewritten.
type SandboxGate interface {
	Gate(ctx context.Context, m *Message, raw func() (io.ReadCloser, error)) (handled bool, rewriteTo []string, err error)
}

// RawStore is the object storage of built messages (`outbound/…`, S1 §7); *objstore.Store implements it.
type RawStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, meta objstore.ObjectMeta) error
	Get(ctx context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error)
}

// Publisher is the bus surface the submit API and the scheduler duties use.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string, headers map[string]string) (bool, error)
}

// job is the JetStream payload of one send attempt (`EMAILBRIDGE_SUBMIT`, S4 §4).
type job struct {
	OutboundID     string `json:"outboundId"`
	AccountID      string `json:"accountId"`
	OrganizationID string `json:"organizationId"`
	AttemptNo      int    `json:"attemptNo"`
}

// EnvelopeInput is the SMTP envelope of a raw submission (SubmitRequest.Envelope).
type EnvelopeInput struct {
	From string   `json:"from,omitempty" doc:"MAIL FROM; default: the From header" example:"bounces@example.com"`
	To   []string `json:"to" minItems:"1" doc:"RCPT TO addresses" example:"[\"anna.rossi@example.org\",\"archive@example.com\"]"`
}

// APIMail is a message handed to an API gateway: the transmitted raw copy and its envelope.
type APIMail struct {
	From       string
	To, Cc     []string
	Bcc        []string
	Raw        []byte
	OutboundID string
	MessageID  string
}
