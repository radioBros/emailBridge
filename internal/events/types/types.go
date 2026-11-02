// Package types holds the Go types of every event's `data` (S3 §3). The JSON Schemas in api/events are
// generated from these types (tools/eventschema) and CI fails on drift, so these types are the contract.
// Field names follow S3 exactly; consumers must ignore unknown fields (additive changes keep the version).
package types

import (
	"time"

	"github.com/radiobros/emailbridge/internal/content"
)

// Address is an e-mail address with an optional display name (S2 §3).
type Address struct {
	Name    string `json:"name,omitempty" doc:"Display name"`
	Address string `json:"address" doc:"E-mail address"`
}

// AccountRef is common to every account event (S3 §3.2).
type AccountRef struct {
	AccountID  string  `json:"accountId" doc:"Account ID (acc_…)"`
	ExternalID *string `json:"externalId" doc:"Caller-supplied external ID, or null"`
}

// Mailbox identifies a folder in message and mailbox events.
type Mailbox struct {
	MailboxID  string `json:"mailboxId" doc:"Mailbox ID (mbx_…)"`
	Path       string `json:"path" doc:"Folder path as on the server"`
	SpecialUse string `json:"specialUse,omitempty" doc:"RFC 6154 special use (\\\\Sent, \\\\Trash, …)"`
}

// Reason explains a state change or failure.
type Reason struct {
	Kind    string `json:"kind" doc:"Error kind (S0 error model)"`
	Code    string `json:"code" doc:"Problem code"`
	Message string `json:"message,omitempty" doc:"Human-readable, redacted"`
}

// ---- account events (S3 §3.3) ----

// AccountAdded is account.added.
type AccountAdded struct {
	AccountRef
	Email string `json:"email" doc:"Mailbox address of the new account"`
	Name  string `json:"name,omitempty" doc:"Display name of the account; omitted when not set"`
}

// AccountUpdated is account.updated (no credentials).
type AccountUpdated struct {
	AccountRef
	Changed []string `json:"changed" doc:"Top-level field names that changed"`
}

// AccountDeleted is account.deleted (erasure finished).
type AccountDeleted struct {
	AccountRef
}

// AccountInitialized is account.initialized (first full sync completed).
type AccountInitialized struct {
	AccountRef
	Mailboxes  int   `json:"mailboxes" doc:"Folders indexed by the first full sync"`
	Messages   int   `json:"messages" doc:"Messages indexed by the first full sync"`
	DurationMs int64 `json:"durationMs" doc:"Duration of the first full sync in milliseconds"`
}

// AccountStateChanged is account.state_changed (S5 §2 states).
type AccountStateChanged struct {
	AccountRef
	From   string  `json:"from" doc:"Previous connection state (S5 §2: new, connecting, syncing, connected, authError, connectError, disconnected, disabled, deleting)"`
	To     string  `json:"to" doc:"New connection state"`
	Reason *Reason `json:"reason,omitempty" doc:"Why the state changed; omitted for a normal transition"`
}

// AccountAuthFailed is account.auth_failed.
type AccountAuthFailed struct {
	AccountRef
	Protocol     string     `json:"protocol" enum:"imap,smtp" doc:"Protocol whose login failed"`
	Code         string     `json:"code" doc:"Problem code of the failure, e.g. mail_auth_failed or oauth_revoked"`
	Response     string     `json:"response,omitempty" doc:"Redacted server text"`
	FailingSince time.Time  `json:"failingSince" doc:"When the current run of failed logins started"`
	DisableAt    *time.Time `json:"disableAt,omitempty" doc:"When the account will be disabled if failures continue"`
}

// AccountAuthSucceeded is account.auth_succeeded.
type AccountAuthSucceeded struct {
	AccountRef
	Protocol  string `json:"protocol" enum:"imap,smtp" doc:"Protocol whose login works again"`
	FailedFor string `json:"failedFor" doc:"Duration of the failure period (Go duration string)"`
}

// AccountConnectFailed is account.connect_failed.
type AccountConnectFailed struct {
	AccountRef
	Protocol string `json:"protocol" enum:"imap,smtp" doc:"Protocol whose connection failed"`
	Code     string `json:"code" doc:"Problem code, e.g. mail_connect_failed, mail_tls_failed or mail_timeout"`
	Kind     string `json:"kind" doc:"Error kind (S0 error model), e.g. temporary or config"`
	Response string `json:"response,omitempty" doc:"Redacted server text or network error"`
}

// ---- mailbox events (S3 §3.4) ----

// MailboxChanged is mailbox.created and mailbox.deleted.
type MailboxChanged struct {
	AccountRef
	Mailbox Mailbox `json:"mailbox" doc:"The folder that was created or deleted"`
}

// MailboxReset is mailbox.reset (UIDVALIDITY changed).
type MailboxReset struct {
	AccountRef
	Mailbox        Mailbox `json:"mailbox" doc:"The folder whose UIDVALIDITY changed"`
	OldUIDValidity uint32  `json:"oldUidValidity" doc:"UIDVALIDITY before the reset"`
	NewUIDValidity uint32  `json:"newUidValidity" doc:"UIDVALIDITY after the reset"`
	Messages       int     `json:"messages" doc:"Messages re-indexed"`
}

// ---- inbound message events (S3 §3.5) ----

// TextContent is the optional text part of message.received.
type TextContent struct {
	Plain     string `json:"plain,omitempty" doc:"Plain-text body, when the endpoint asked for it"`
	HTML      string `json:"html,omitempty" doc:"HTML body, when the endpoint asked for it"`
	Truncated bool   `json:"truncated" doc:"True when a body was cut at the endpoint's text limit"`
	Size      int64  `json:"size" doc:"Size of the full text in bytes before truncation"`
}

// AttachmentInfo is attachment metadata (content via the API).
type AttachmentInfo struct {
	ID          string `json:"id,omitempty" doc:"Attachment ID (att_…)"` // att_… (P4.3)
	Filename    string `json:"filename,omitempty" doc:"File name from the MIME part"`
	ContentType string `json:"contentType" doc:"MIME type of the part"`
	Size        int64  `json:"size" doc:"Decoded size in bytes"`
	ContentID   string `json:"contentId,omitempty" doc:"Content-ID for inline parts"`
	Disposition string `json:"disposition,omitempty" doc:"attachment or inline"`
	PartID      string `json:"partId" doc:"MIME part number (e.g. 2 or 1.2)"`
	URL         string `json:"url,omitempty" doc:"Signed download URL, when attachments are stored in object storage"` // signed URL when attachments.s3 stored the object
	Scan        string `json:"scan,omitempty" doc:"Malware scan result: clean, infected, error or skipped"`            // clean|infected|error|skipped (P4.3)
}

// AuthResult is one SPF/DKIM/DMARC/ARC verdict.
type AuthResult struct {
	Result   string `json:"result" doc:"Verdict: pass, fail, softfail, neutral, none, temperror or permerror"`
	Domain   string `json:"domain,omitempty" doc:"Domain that was checked"`
	Selector string `json:"selector,omitempty" doc:"DKIM selector"`
}

// AuthResults are the inbound authentication results (inbound.authResults).
type AuthResults struct {
	SPF    *AuthResult  `json:"spf,omitempty" doc:"SPF verdict"`
	DKIM   []AuthResult `json:"dkim" doc:"One verdict per DKIM signature"`
	DMARC  *AuthResult  `json:"dmarc,omitempty" doc:"DMARC verdict"`
	ARC    *AuthResult  `json:"arc,omitempty" doc:"ARC chain verdict"`
	Source string       `json:"source" enum:"header,verify" doc:"header: taken from Authentication-Results; verify: computed by EmailBridge"`
}

// MessageReceived is message.received.
type MessageReceived struct {
	AccountRef
	MessageID       string                  `json:"messageId" doc:"Our ID (msg_…)"`
	Mailbox         Mailbox                 `json:"mailbox" doc:"Folder the message arrived in"`
	UID             uint32                  `json:"uid" doc:"IMAP UID in the folder"`
	UIDValidity     uint32                  `json:"uidValidity" doc:"UIDVALIDITY of the folder"`
	EmailID         string                  `json:"emailId" doc:"Stable message identity across folders (Gmail X-GM-MSGID or a content hash)"`
	ThreadID        string                  `json:"threadId" doc:"Conversation ID"`
	MessageIDHeader string                  `json:"messageIdHeader" doc:"Message-ID header, unchanged"`
	InReplyTo       string                  `json:"inReplyTo,omitempty" doc:"In-Reply-To header"`
	References      []string                `json:"references,omitempty" doc:"Message-IDs of the References header"`
	Date            *time.Time              `json:"date" doc:"Date header, null if unparsable"`
	InternalDate    time.Time               `json:"internalDate" doc:"IMAP INTERNALDATE (when the server received the message)"`
	Size            int64                   `json:"size" doc:"RFC 822 size in bytes"`
	Flags           []string                `json:"flags" doc:"IMAP flags"`
	Labels          []string                `json:"labels" doc:"Gmail labels (empty on other servers)"`
	From            *Address                `json:"from" doc:"From address; null when missing"`
	Sender          *Address                `json:"sender,omitempty" doc:"Sender header, when different from From"`
	To              []Address               `json:"to" doc:"To recipients"`
	Cc              []Address               `json:"cc" doc:"Cc recipients"`
	Bcc             []Address               `json:"bcc,omitempty" doc:"Bcc recipients (only on messages the account sent)"`
	ReplyTo         []Address               `json:"replyTo,omitempty" doc:"Reply-To addresses"`
	Subject         string                  `json:"subject" doc:"Decoded Subject header"`
	Headers         map[string][]string     `json:"headers,omitempty" doc:"Lower-cased header names the endpoint asked for"`
	Text            *TextContent            `json:"text,omitempty" doc:"Message text, when the endpoint's payload options include it"`
	Attachments     []AttachmentInfo        `json:"attachments,omitempty" doc:"Attachment metadata, when the endpoint's payload options include it"`
	AuthResults     *AuthResults            `json:"authResults,omitempty" doc:"SPF, DKIM, DMARC and ARC results"`
	WebSafeHTML     string                  `json:"webSafeHtml,omitempty" doc:"Sanitised HTML body (P4): no scripts, handlers, forms or external CSS; links open in a new tab"`
	CalendarEvents  []content.CalendarEvent `json:"calendarEvents,omitempty" doc:"Parsed text/calendar parts (P4): invitations, updates, cancellations"`
	ReplyText       string                  `json:"replyText,omitempty" doc:"New reply text with quoted history stripped (P4.2 / W48)"`
	IsAutoReply     bool                    `json:"isAutoReply,omitempty" doc:"Automatic reply (RFC 3834, out-of-office), inbound.bounceDetection on (P5)"`
	Bounce          *BounceInfo             `json:"bounce,omitempty" doc:"Bounce or complaint classification, inbound.bounceDetection on (P5)"`
	Spam            *SpamInfo               `json:"spam,omitempty" doc:"Rspamd score/action/symbols when integrations.rspamd is on (P8.3)"`
	PEC             *PECInfo                `json:"pec,omitempty" doc:"PEC envelope or receipt, pec.enabled accounts (P9, S17 §4)"`
	SeenBefore      bool                    `json:"seenBefore" doc:"Same emailId already exists in another folder (a move)"`
}

// SpamInfo is message.received.spam from Rspamd /checkv2 (P8.3).
type SpamInfo struct {
	Score   float64            `json:"score" doc:"Rspamd score"`
	Action  string             `json:"action,omitempty" doc:"Rspamd action, e.g. no action, add header, reject"`
	Symbols map[string]float64 `json:"symbols,omitempty" doc:"Matched Rspamd symbols with their scores"`
}

// FlagChange is the added/removed/current triple of message.updated.
type FlagChange struct {
	Added   []string `json:"added" doc:"Values added by this change"`
	Removed []string `json:"removed" doc:"Values removed by this change"`
	Current []string `json:"current" doc:"All values after the change"`
}

// MessageUpdated is message.updated.
type MessageUpdated struct {
	AccountRef
	MessageID string      `json:"messageId" doc:"Our ID (msg_…)"`
	Mailbox   Mailbox     `json:"mailbox" doc:"Folder of the message"`
	UID       uint32      `json:"uid" doc:"IMAP UID in the folder"`
	Flags     FlagChange  `json:"flags" doc:"IMAP flag changes"`
	Labels    *FlagChange `json:"labels,omitempty" doc:"P6 (Gmail labels)"`
}

// MessageDeleted is message.deleted.
type MessageDeleted struct {
	AccountRef
	MessageID       string  `json:"messageId" doc:"Our ID (msg_…)"`
	Mailbox         Mailbox `json:"mailbox" doc:"Folder the message was removed from"`
	UID             uint32  `json:"uid" doc:"IMAP UID it had"`
	MessageIDHeader string  `json:"messageIdHeader" doc:"Message-ID header"`
}

// MessageMissing is message.missing.
type MessageMissing struct {
	AccountRef
	Mailbox         Mailbox `json:"mailbox" doc:"Folder where the message was expected"`
	UID             uint32  `json:"uid" doc:"IMAP UID that disappeared"`
	MessageIDHeader string  `json:"messageIdHeader,omitempty" doc:"Message-ID header, when known"`
}

// ---- outbound message events (S3 §3.6) ----

// Envelope is the SMTP envelope summary (cc/bcc addresses are not repeated).
type Envelope struct {
	From string   `json:"from" doc:"SMTP MAIL FROM address"`
	To   []string `json:"to" doc:"SMTP RCPT TO addresses"`
}

// OutboundRef is common to outbound events.
type OutboundRef struct {
	AccountRef
	OutboundID      string   `json:"outboundId" doc:"Outbound ID (out_…)"`
	MessageIDHeader string   `json:"messageIdHeader" doc:"Message-ID header of the sent message"`
	Envelope        Envelope `json:"envelope" doc:"SMTP envelope"`
	Subject         string   `json:"subject" doc:"Subject of the message"`
}

// SMTPReply is the final SMTP reply of a sent message.
type SMTPReply struct {
	Host     string `json:"host" doc:"SMTP server that answered"`
	Code     int    `json:"code" doc:"SMTP reply code"`
	Enhanced string `json:"enhanced,omitempty" doc:"Enhanced status code (RFC 3463)"`
	Response string `json:"response" doc:"Server reply text"`
}

// SendError describes a failed attempt.
type SendError struct {
	Code     string `json:"code" doc:"Problem code, e.g. smtp_permanent, smtp_temporary or mail_connect_failed"`
	Kind     string `json:"kind" doc:"Error kind (S0 error model): temporary, permanent, rate_limited or outcome_unknown"`
	Stage    string `json:"stage" enum:"connect,ehlo,auth,mailFrom,rcptTo,data,dataEnd" doc:"SMTP stage that failed"`
	SMTPCode int    `json:"smtpCode,omitempty" doc:"SMTP reply code, when the server answered"`
	Enhanced string `json:"enhanced,omitempty" doc:"Enhanced status code (RFC 3463)"`
	Response string `json:"response,omitempty" doc:"Redacted server reply"`
}

// Rejection is a recipient refused while others were accepted.
type Rejection struct {
	Address  string `json:"address" doc:"Refused recipient"`
	Code     int    `json:"code" doc:"SMTP reply code"`
	Enhanced string `json:"enhanced,omitempty" doc:"Enhanced status code (RFC 3463)"`
	Message  string `json:"message" doc:"Server reply text"`
}

// MessageSent is message.sent.
type MessageSent struct {
	OutboundRef
	SentAt    time.Time   `json:"sentAt" doc:"When the server accepted the message"`
	Attempt   int         `json:"attempt" doc:"Attempt number that succeeded (1 = first try)"`
	SMTP      SMTPReply   `json:"smtp" doc:"Final SMTP reply"`
	Rejected  []Rejection `json:"rejected,omitempty" doc:"Recipients refused while the others were accepted"`
	GatewayID string      `json:"gatewayId,omitempty" doc:"P5"`
}

// MessageDeliveryAttemptFailed is message.delivery_attempt_failed.
type MessageDeliveryAttemptFailed struct {
	OutboundRef
	Attempt       int       `json:"attempt" doc:"Attempt number that failed"`
	MaxAttempts   int       `json:"maxAttempts" doc:"Attempts allowed before the message fails"`
	NextAttemptAt time.Time `json:"nextAttemptAt" doc:"When the next attempt is scheduled"`
	Error         SendError `json:"error" doc:"Why the attempt failed"`
}

// MessageFailed is message.failed.
type MessageFailed struct {
	OutboundRef
	Attempts       int       `json:"attempts" doc:"Attempts made"`
	OutcomeUnknown bool      `json:"outcomeUnknown" doc:"No final reply after DATA under atMostOnce"`
	Error          SendError `json:"error" doc:"Error of the last attempt"`
}

// MessageCaptured is message.captured (sandbox).
type MessageCaptured struct {
	OutboundRef
	CaptureID string `json:"captureId" doc:"Sandbox capture ID (cap_…)"`
	Policy    string `json:"policy" enum:"capture,rewrite,drop" doc:"Sandbox policy that applied"`
}

// EndpointTest is endpoint.test (synthetic; never stored or routed).
type EndpointTest struct {
	EndpointID string    `json:"endpointId" doc:"Webhook endpoint ID (whe_…)"`
	Message    string    `json:"message" doc:"Fixed test text"`
	SentAt     time.Time `json:"sentAt" doc:"When the test was sent"`
}

// BounceInfo classifies an inbound bounce or complaint on message.received (P5.4).
type BounceInfo struct {
	Kind              string            `json:"kind" doc:"dsn_bounce | heuristic_bounce | complaint | challenge_response"`
	BounceType        string            `json:"bounceType,omitempty" doc:"hard | soft"`
	OutboundID        string            `json:"outboundId,omitempty" doc:"The outbound message it refers to, when correlated"`
	OriginalMessageID string            `json:"originalMessageId,omitempty" doc:"Message-ID of the original message"`
	Recipients        []BounceRecipient `json:"recipients,omitempty" doc:"Per-recipient rows of the DSN"`
}

// BounceRecipient is one recipient row of a bounce.
type BounceRecipient struct {
	Address    string `json:"address" doc:"Recipient that bounced"`
	Action     string `json:"action,omitempty" doc:"DSN action: failed, delayed, delivered, relayed or expanded"`
	Status     string `json:"status,omitempty" doc:"Enhanced status code, e.g. 5.1.1"`
	Diagnostic string `json:"diagnostic,omitempty" doc:"Remote diagnostic, at most 512 characters"`
	BounceType string `json:"bounceType,omitempty" doc:"hard or soft"`
}

// MessageBounced is message.bounced (P5.4): one event per bounced recipient.
type MessageBounced struct {
	AccountRef
	MessageID       string `json:"messageId" doc:"The bounce message (msg_…); empty when a sending provider reported the bounce (then the event subject is the outbound message)"`
	OutboundID      string `json:"outboundId,omitempty" doc:"The outbound message that bounced, when correlated"`
	MessageIDHeader string `json:"messageIdHeader,omitempty" doc:"Message-ID of the original message, when known"`
	Recipient       string `json:"recipient" doc:"Recipient that bounced"`
	Action          string `json:"action" doc:"DSN action, e.g. failed or delayed"`
	Status          string `json:"status,omitempty" doc:"Enhanced status code, e.g. 5.1.1"`
	Diagnostic      string `json:"diagnostic,omitempty" doc:"Remote diagnostic, at most 512 characters"`
	BounceType      string `json:"bounceType" doc:"hard | soft"`
	MTA             string `json:"mta,omitempty" doc:"Reporting MTA"`
}

// MessageComplained is message.complained (P5.4): an ARF feedback report.
type MessageComplained struct {
	AccountRef
	MessageID       string `json:"messageId" doc:"The complaint message (msg_…); empty when a sending provider reported the complaint"`
	OutboundID      string `json:"outboundId,omitempty" doc:"The outbound message complained about, when correlated"`
	MessageIDHeader string `json:"messageIdHeader,omitempty" doc:"Message-ID of the original message, when known"`
	Recipient       string `json:"recipient,omitempty" doc:"Recipient who complained, when the report names one"`
	FeedbackType    string `json:"feedbackType,omitempty" doc:"ARF Feedback-Type, e.g. abuse"`
}

// TrackingOpened is tracking.opened (P5.2): open-pixel hit (S3 §3.7).
type TrackingOpened struct {
	AccountRef
	OutboundID string `json:"outboundId" doc:"Outbound ID (out_…)"`
	Recipient  string `json:"recipient" doc:"Recipient whose pixel was loaded"`
	IP         string `json:"ip,omitempty" doc:"Truncated client IP"`
	UserAgent  string `json:"userAgent,omitempty" doc:"Client User-Agent"`
	Bot        bool   `json:"bot" doc:"True when prefetch/bot heuristics matched"`
}

// TrackingClicked is tracking.clicked (P5.2): tracked link click (S3 §3.7).
type TrackingClicked struct {
	AccountRef
	OutboundID string `json:"outboundId" doc:"Outbound ID (out_…)"`
	Recipient  string `json:"recipient" doc:"Recipient who clicked"`
	URL        string `json:"url" doc:"Original destination URL"`
	IP         string `json:"ip,omitempty" doc:"Truncated client IP"`
	UserAgent  string `json:"userAgent,omitempty" doc:"Client User-Agent"`
	Bot        bool   `json:"bot" doc:"True when prefetch/bot heuristics matched"`
}

// ListUnsubscribed is list.unsubscribed (P5.2): one-click or link unsubscribe (S3 §3.7).
type ListUnsubscribed struct {
	AccountRef
	OutboundID string `json:"outboundId" doc:"Outbound ID (out_…)"`
	Recipient  string `json:"recipient" doc:"Recipient who unsubscribed"`
	IP         string `json:"ip,omitempty" doc:"Truncated client IP"`
	UserAgent  string `json:"userAgent,omitempty" doc:"Client User-Agent"`
	Bot        bool   `json:"bot,omitempty" doc:"True when prefetch/bot heuristics matched"`
}

// ExportCompleted is export.completed (P7.2): bulk export ready for download.
type ExportCompleted struct {
	AccountRef
	ExportID     string `json:"exportId" doc:"Export ID (exp_…)"`
	MessageCount int64  `json:"messageCount" doc:"Messages in the export"`
	ByteSize     int64  `json:"byteSize" doc:"Encrypted object size in bytes"`
}

// ExportFailed is export.failed (P7.2): bulk export failed.
type ExportFailed struct {
	AccountRef
	ExportID string `json:"exportId" doc:"Export ID (exp_…)"`
	Code     string `json:"code" doc:"Problem code"`
	Message  string `json:"message,omitempty" doc:"Human-readable, redacted"`
}

// PECSignature is the S/MIME check of a PEC message (S17 §7).
type PECSignature struct {
	Valid   bool  `json:"valid" doc:"The S/MIME signature verifies"`
	Trusted *bool `json:"trusted" doc:"Signer chains to a configured trust anchor; null when no anchors are configured"`
}

// PECInfo is message.received.pec (S17 §4): what a PEC system message is.
type PECInfo struct {
	Kind              string       `json:"kind" enum:"envelope,anomaly,receipt" doc:"envelope: a certified message (busta di trasporto); anomaly: busta di anomalia; receipt: a PEC receipt"`
	Type              string       `json:"type" doc:"X-Ricevuta / X-Trasporto value, e.g. accettazione, avvenuta-consegna, posta-certificata"`
	ReceiptKind       string       `json:"receiptKind,omitempty" enum:"completa,breve,sintetica" doc:"Receipt variant requested by the sender"`
	TransportID       string       `json:"transportId,omitempty" doc:"PEC transport identifier (identificativo)"`
	OriginalMessageID string       `json:"originalMessageId,omitempty" doc:"Message-ID of the original message"`
	DeliveredTo       string       `json:"deliveredTo,omitempty" doc:"Recipient the receipt confirms"`
	Error             string       `json:"error,omitempty" doc:"daticert errore (nessuno = none)"`
	ReceiptID         string       `json:"receiptId,omitempty" doc:"rcp_… for receipts"`
	Signature         PECSignature `json:"signature" doc:"S/MIME check of the PEC message"`
	ParseError        string       `json:"parseError,omitempty" doc:"Why daticert.xml could not be parsed, when it could not"`
}

// PECRecipient is one addressee of a certified message.
type PECRecipient struct {
	Address string `json:"address" doc:"Recipient address"`
	Kind    string `json:"kind" enum:"certificato,esterno" doc:"certificato: a PEC mailbox; esterno: a normal mailbox"`
}

// SystemAlertRaised is system.alert.raised (W75).
type SystemAlertRaised struct {
	NotificationID string            `json:"notificationId" doc:"Notification ID (ntf_…)"`
	Key            string            `json:"key" doc:"Dedup key"`
	Category       string            `json:"category" doc:"Alert category, e.g. webhooks, accounts or storage"`
	Severity       string            `json:"severity" enum:"info,warning,critical" doc:"Alert severity"`
	Scope          string            `json:"scope" doc:"org_… or instance"`
	TitleKey       string            `json:"titleKey" doc:"i18n key of the title"`
	DetailKey      string            `json:"detailKey" doc:"i18n key of the detail text"`
	Link           string            `json:"link,omitempty" doc:"Admin UI path to the affected resource"`
	Occurrences    int               `json:"occurrences" doc:"How often the condition was seen since it was raised"`
	Params         map[string]string `json:"params,omitempty" doc:"Values for the title and detail texts"`
}

// SystemAlertResolved is system.alert.resolved (W75).
type SystemAlertResolved struct {
	NotificationID string    `json:"notificationId" doc:"Notification ID (ntf_…)"`
	Key            string    `json:"key" doc:"Dedup key of the alert"`
	Category       string    `json:"category" doc:"Alert category"`
	Scope          string    `json:"scope" doc:"org_… or instance"`
	ResolvedAt     time.Time `json:"resolvedAt" doc:"When the condition cleared"`
}

// PECReceipt is pec.receipt (S17 §6).
type PECReceipt struct {
	AccountRef
	ReceiptID          string         `json:"receiptId" doc:"rcp_…"`
	MessageID          string         `json:"messageId" doc:"The receipt in the index (msg_…)"`
	OutboundID         string         `json:"outboundId,omitempty" doc:"Linked outbound message, when EmailBridge sent the original"`
	Type               string         `json:"type" doc:"accettazione, non-accettazione, presa-in-carico, avvenuta-consegna, errore-consegna, preavviso-errore-consegna, rilevazione-virus"`
	ReceiptKind        string         `json:"receiptKind,omitempty" doc:"completa, breve or sintetica"`
	Error              string         `json:"error,omitempty" doc:"daticert errore value (nessuno = none)"`
	ErrorDetail        string         `json:"errorDetail,omitempty" doc:"daticert errore-esteso text"`
	TransportID        string         `json:"transportId" doc:"PEC transport identifier (identificativo)"`
	RefMessageIDHeader string         `json:"refMessageIdHeader,omitempty" doc:"X-Riferimento-Message-ID: the original's Message-ID"`
	DeliveredTo        string         `json:"deliveredTo,omitempty" doc:"Recipient the receipt confirms"`
	Recipients         []PECRecipient `json:"recipients,omitempty" doc:"Addressees listed in daticert.xml"`
	Provider           string         `json:"provider,omitempty" doc:"PEC provider that issued the receipt"`
	At                 time.Time      `json:"at" doc:"Date and time in the receipt"`
	Signature          PECSignature   `json:"signature" doc:"S/MIME check of the receipt"`
	SHA256             string         `json:"sha256" doc:"SHA-256 of the raw receipt (hex)"`
	PECState           string         `json:"pecState,omitempty" doc:"Outbound PEC state after this receipt (S17 §5.2)"`
}
