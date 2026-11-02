package messages

import (
	"time"

	"github.com/radiobros/emailbridge/internal/submit"
)

// Collections.
const (
	Drafts = "drafts"
)

const (
	maxList     = 200
	maxMbxList  = 100
	maxBatch    = 1000
	uidChunk    = 200
	defaultText = 256 << 10
	hardTextCap = 10 << 20
	maxUpload   = 25 << 20
)

// MailboxView is the API representation of an indexed mailbox.
type MailboxView struct {
	ID            string     `json:"id" doc:"Mailbox ID (mbx_…). Empty in a create response until the sync engine has indexed the new folder." example:"mbx_01m5ch2yg0he3zdbefkydy9cjb"`
	AccountID     string     `json:"accountId" doc:"Account the mailbox belongs to (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Path          string     `json:"path" doc:"Full folder path on the server, levels joined by delimiter." example:"Archive/2026"`
	Delimiter     string     `json:"delimiter,omitempty" doc:"Hierarchy delimiter the server uses in path." example:"/"`
	SpecialUse    string     `json:"specialUse,omitempty" doc:"RFC 6154 special-use attribute (\\Inbox, \\Sent, \\Drafts, \\Trash, \\Junk, \\Archive, \\All, \\Flagged)." example:"\\Archive"`
	UIDValidity   uint32     `json:"uidValidity" doc:"IMAP UIDVALIDITY of the folder; when it changes every UID in it is new." example:"1729500000"`
	UIDNext       uint32     `json:"uidNext,omitempty" doc:"Next UID the server will assign (as last seen by sync)." example:"4812"`
	HighestModSeq uint64     `json:"highestModseq,omitempty" doc:"CONDSTORE HIGHESTMODSEQ as last seen by sync (absent when the server has no CONDSTORE)." example:"918273"`
	Exists        uint32     `json:"exists,omitempty" doc:"Number of messages in the folder as last seen by sync." example:"4795"`
	SyncMode      string     `json:"syncMode,omitempty" doc:"How sync follows the folder: idle (IMAP IDLE push) or poll." example:"idle"`
	LastSyncAt    *time.Time `json:"lastSyncAt,omitempty" doc:"When sync last finished a pass over this folder." example:"2026-10-27T09:58:12Z"`
	Status        string     `json:"status" doc:"active, or pending in a create response when the folder exists on the server but is not indexed yet." example:"active"`
}

// MessageView is index metadata plus optional on-demand enrichments.
type MessageView struct {
	ID              string            `json:"id" doc:"Message ID (msg_…), stable while the message stays in its folder." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	AccountID       string            `json:"accountId" doc:"Account the message belongs to (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MailboxID       string            `json:"mailboxId" doc:"Folder holding the message (mbx_…)." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	UIDValidity     uint32            `json:"uidValidity" doc:"UIDVALIDITY of the folder when the message was indexed." example:"1729500000"`
	UID             uint32            `json:"uid" doc:"IMAP UID inside the folder (for Gmail / Graph accounts a sync-assigned number)." example:"4811"`
	ModSeq          uint64            `json:"modseq,omitempty" doc:"CONDSTORE MODSEQ of the last flag change seen by sync." example:"918270"`
	Flags           []string          `json:"flags" doc:"IMAP flags and keywords (\\Seen, \\Flagged, \\Answered, \\Draft, $Forwarded …) as indexed; empty array when none." example:"[\"\\\\Seen\",\"$Invoice\"]"`
	MessageIDHeader string            `json:"messageIdHeader,omitempty" doc:"Message-ID header, with angle brackets." example:"<20261027095512.4f2a@example.com>"`
	EmailID         string            `json:"emailId" doc:"Provider-stable message identity (OBJECTID EMAILID / X-GM-MSGID, the Gmail or Graph message ID)." example:"M8e1f4a7c92d5b3e6"`
	ThreadID        string            `json:"threadId,omitempty" doc:"Conversation ID (IMAP THREADID, the Gmail thread ID or the Graph conversationId); filter the list with threadId." example:"T5b0c9d2e71a4f863"`
	InternalDate    time.Time         `json:"internalDate" doc:"Server INTERNALDATE: when the message arrived in the mailbox." example:"2026-10-27T09:55:14Z"`
	Size            int64             `json:"size" doc:"RFC 822 size in bytes." example:"48213"`
	Subject         string            `json:"subject,omitempty" doc:"Decoded Subject header." example:"Invoice 2026-1043"`
	Date            *time.Time        `json:"date,omitempty" doc:"Date header (from the index summary or the envelope)." example:"2026-10-27T09:55:12Z"`
	From            []AddressView     `json:"from,omitempty" doc:"From addresses."`
	To              []AddressView     `json:"to,omitempty" doc:"To addresses."`
	Cc              []AddressView     `json:"cc,omitempty" doc:"Cc addresses; only with envelope=true or text."`
	Text            string            `json:"text,omitempty" doc:"Plain-text body (text=plain or both), at most maxBytes." example:"Please find the invoice attached."`
	HTML            string            `json:"html,omitempty" doc:"HTML body as sent (text=html or both); show webSafeHtml in a browser instead." example:"<p>Please find the invoice <b>2026-1043</b> attached.</p>"`
	WebSafeHTML     string            `json:"webSafeHtml,omitempty" doc:"html sanitised for display (scripts, handlers and remote content removed); returned with html." example:"<p>Please find the invoice <b>2026-1043</b> attached.</p>"`
	Headers         map[string]string `json:"headers,omitempty" doc:"First value of every header (headers=true), keyed by canonical header name." example:"{\"Message-Id\":\"<20261027095512.4f2a@example.com>\",\"Return-Path\":\"<billing@example.com>\",\"X-Mailer\":\"Billing 4.2\"}"`
	TextTruncated   bool              `json:"textTruncated,omitempty" doc:"true when text / html were cut at maxBytes." example:"false"`
	Attachments     []AttachmentView  `json:"attachments,omitempty" doc:"Attachments recorded for the message (from the attachments index)."`
	Categories      []string          `json:"categories,omitempty" doc:"Gmail CATEGORY_* labels" example:"[\"CATEGORY_UPDATES\"]"`
	Labels          []string          `json:"labels,omitempty" doc:"Gmail user labels" example:"[\"Invoices\"]"`
}

// AttachmentView is one attachment row from the attachments collection (W50 / G10).
type AttachmentView struct {
	ID          string `json:"id" doc:"Attachment ID (att_…); download it with GET /v1/accounts/{accountId}/messages/{messageId}/attachments/{attachmentId}." example:"att_01m5cnaee0p3aqj73d3n6ej38p"`
	Filename    string `json:"filename,omitempty" doc:"Decoded file name." example:"invoice-2026-1043.pdf"`
	ContentType string `json:"contentType" doc:"MIME type of the part." example:"application/pdf"`
	Size        int64  `json:"size" doc:"Decoded size in bytes." example:"38412"`
	ContentID   string `json:"contentId,omitempty" doc:"Content-ID of an inline part (without angle brackets)." example:"logo@example.com"`
	Inline      bool   `json:"inline,omitempty" doc:"true for an inline part referenced from the HTML by cid:." example:"false"`
	Scan        string `json:"scan,omitempty" doc:"Virus-scan verdict when scanning is on: clean, infected, error or skipped." example:"clean"`
	URL         string `json:"url,omitempty" doc:"Download URL, when the response carries one." example:"/v1/accounts/acc_01m5cvnpb031g57sqct6mzhq94/messages/msg_01m5ck6pf04wm1cyx6q10s4x5w/attachments/att_01m5cnaee0p3aqj73d3n6ej38p"`
}

// AddressView is a display address.
type AddressView struct {
	Name    string `json:"name,omitempty" doc:"Display name." example:"Anna Rossi"`
	Address string `json:"address" doc:"Email address." example:"anna.rossi@example.org"`
}

// DraftView is a drafts collection row.
type DraftView struct {
	ID              string    `json:"id" doc:"Draft ID (drf_…)." example:"drf_01m5cqe6d0nx6xdjvzyprw8twh"`
	AccountID       string    `json:"accountId" doc:"Account the draft belongs to (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Path            string    `json:"path" doc:"Folder the draft was appended to (the \\Drafts special-use folder, else Drafts)." example:"Drafts"`
	UID             uint32    `json:"uid,omitempty" doc:"IMAP UID of the draft in path; absent when the server did not report one (such a draft cannot be sent)." example:"57"`
	UIDValidity     uint32    `json:"uidValidity,omitempty" doc:"UIDVALIDITY of path at save time." example:"1729500003"`
	MessageIDHeader string    `json:"messageIdHeader,omitempty" doc:"Message-ID header of the draft." example:"<quote-58213@example.com>"`
	Subject         string    `json:"subject,omitempty" doc:"Subject of the draft." example:"Quote for the November order"`
	Size            int64     `json:"size" doc:"Size of the stored MIME message in bytes." example:"2841"`
	CreatedAt       time.Time `json:"createdAt" doc:"When the draft was saved." example:"2026-10-27T10:02:41Z"`
	UpdatedAt       time.Time `json:"updatedAt" doc:"Last change of the draft row." example:"2026-10-27T10:02:41Z"`
	Text            string    `json:"text,omitempty" doc:"Plain-text body, only with text=true." example:"Hello Marco,\n\nhere is the quote."`
	HTML            string    `json:"html,omitempty" doc:"HTML body, only with text=true." example:"<p>Hello Marco,</p><p>here is the quote.</p>"`
}

// CreateMailboxInput is POST /mailboxes.
type CreateMailboxInput struct {
	Path      string `json:"path" doc:"Full path of the new folder, using the server's delimiter (required)." example:"Archive/2026"`
	Subscribe *bool  `json:"subscribe,omitempty" doc:"Also SUBSCRIBE the folder (default true)." example:"true"`
}

// PatchMailboxInput is PATCH /mailboxes/{id}.
type PatchMailboxInput struct {
	Path       *string `json:"path,omitempty" doc:"New full path (IMAP RENAME). The renamed folder gets a new mailbox ID after the next sync." example:"Archive/2026-Q4"`
	Subscribed *bool   `json:"subscribed,omitempty" doc:"SUBSCRIBE (true) or UNSUBSCRIBE (false) the folder." example:"true"`
}

// PatchMessageInput is PATCH /messages/{id}.
type PatchMessageInput struct {
	AddFlags    []string `json:"addFlags,omitempty" doc:"Flags / keywords to add." example:"[\"\\\\Seen\",\"\\\\Flagged\"]"`
	RemoveFlags []string `json:"removeFlags,omitempty" doc:"Flags / keywords to remove." example:"[\"$Junk\"]"`
	SetFlags    []string `json:"setFlags,omitempty" doc:"Replace all flags with exactly this list (wins over addFlags / removeFlags)." example:"[\"\\\\Seen\",\"\\\\Answered\"]"`
}

// MoveInput is POST …/move.
type MoveInput struct {
	MailboxID string `json:"mailboxId,omitempty" doc:"Destination mailbox (mbx_…). Give mailboxId or path." example:"mbx_01m5ch2yg0he3zdbefkydy9cjb"`
	Path      string `json:"path,omitempty" doc:"Destination folder path, used when mailboxId is not given (also a folder not indexed yet)." example:"Archive/2026"`
}

// UploadInput is POST /messages (APPEND).
type UploadInput struct {
	MailboxID    string     `json:"mailboxId,omitempty" doc:"Target mailbox (mbx_…). Give mailboxId or path." example:"mbx_01m5ch2yg0he3zdbefkydy9cjb"`
	Path         string     `json:"path,omitempty" doc:"Target folder path, used when mailboxId is not given." example:"Archive/2026"`
	Raw          string     `json:"raw" doc:"The complete RFC 822 message, base64 (standard alphabet), at most 25 MiB decoded (required)." example:"RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg=="` // base64
	Flags        []string   `json:"flags,omitempty" doc:"Flags to set on the appended message." example:"[\"\\\\Seen\"]"`
	InternalDate *time.Time `json:"internalDate,omitempty" doc:"INTERNALDATE for the message (default: now)." example:"2026-10-27T09:55:14Z"`
	UIDValidity  *uint32    `json:"uidValidity,omitempty" doc:"Expected UIDVALIDITY of the target folder; a different current value is 409 uid_validity_mismatch (default: the indexed value)." example:"1729500000"`
}

// UploadResult is the 201 body of an upload.
type UploadResult struct {
	UID         uint32 `json:"uid" doc:"UID the server assigned (UIDPLUS); 0 when the server does not report it." example:"4812"`
	UIDValidity uint32 `json:"uidValidity" doc:"UIDVALIDITY of the target folder." example:"1729500000"`
	Path        string `json:"path" doc:"Folder the message was appended to." example:"Archive/2026"`
}

// BatchInput is POST /messages/batch.
type BatchInput struct {
	IDs         []string `json:"ids" doc:"Message IDs (msg_…), 1–1000; they may be in different folders." example:"[\"msg_01m5ck6pf04wm1cyx6q10s4x5w\",\"msg_01m5cshyc0q8tywzeefhqwnkz7\"]"`
	Action      string   `json:"action" doc:"flags, move, copy or delete." example:"move"` // flags | move | delete | copy
	AddFlags    []string `json:"addFlags,omitempty" doc:"action=flags: flags to add." example:"[\"\\\\Seen\"]"`
	RemoveFlags []string `json:"removeFlags,omitempty" doc:"action=flags: flags to remove." example:"[\"\\\\Flagged\"]"`
	SetFlags    []string `json:"setFlags,omitempty" doc:"action=flags: replace all flags (wins over addFlags / removeFlags)." example:"[\"\\\\Seen\"]"`
	MailboxID   string   `json:"mailboxId,omitempty" doc:"action=move / copy: destination mailbox (mbx_…)." example:"mbx_01m5ch2yg0he3zdbefkydy9cjb"`
	Path        string   `json:"path,omitempty" doc:"action=move / copy: destination folder path, used when mailboxId is not given." example:"Archive/2026"`
}

// BatchResult summarises a batch call.
type BatchResult struct {
	Processed int      `json:"processed" doc:"Number of messages the action was applied to." example:"2"`
	Failed    []string `json:"failed,omitempty" doc:"IDs that were unknown or whose folder could not be changed." example:"[\"msg_01m5czx690tcwegdb2zfz7cnzn\"]"`
}

// SearchInput is POST /messages/search.
type SearchInput struct {
	MailboxID        string      `json:"mailboxId,omitempty" doc:"Folder to search (mbx_…). Give mailboxId or path." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	Path             string      `json:"path,omitempty" doc:"Folder path to search, used when mailboxId is not given." example:"INBOX"`
	Text             string      `json:"text,omitempty" doc:"Text anywhere in headers or body (IMAP TEXT)." example:"invoice"`
	Subject          string      `json:"subject,omitempty" doc:"Substring of the Subject." example:"2026-1043"`
	From             string      `json:"from,omitempty" doc:"Substring of From." example:"billing@example.com"`
	To               string      `json:"to,omitempty" doc:"Substring of To." example:"anna.rossi@example.org"`
	Seen             *bool       `json:"seen,omitempty" doc:"true: only messages with \\Seen." example:"true"`
	Unseen           *bool       `json:"unseen,omitempty" doc:"true: only messages without \\Seen." example:"false"`
	Flagged          *bool       `json:"flagged,omitempty" doc:"true: only messages with \\Flagged." example:"false"`
	Since            *time.Time  `json:"since,omitempty" doc:"Received on or after this day (IMAP SINCE compares the date part)." example:"2026-10-01T00:00:00Z"`
	Before           *time.Time  `json:"before,omitempty" doc:"Received before this day (IMAP BEFORE)." example:"2026-11-01T00:00:00Z"`
	Header           *HeaderCrit `json:"header,omitempty" doc:"Match one header (IMAP HEADER)."`
	Larger           *int64      `json:"larger,omitempty" doc:"Larger than this many bytes (0–100 MiB)." example:"10240"`
	Smaller          *int64      `json:"smaller,omitempty" doc:"Smaller than this many bytes (0–100 MiB)." example:"5242880"`
	UIDMin           *uint32     `json:"uidMin,omitempty" doc:"Lowest UID (inclusive)." example:"4000"`
	UIDMax           *uint32     `json:"uidMax,omitempty" doc:"Highest UID (inclusive)." example:"4900"`
	Labels           []string    `json:"labels,omitempty" doc:"Gmail labels the message must carry (≤ 20, each ≤ 128 characters)." example:"[\"Invoices\"]"`
	GmailRaw         string      `json:"gmailRaw,omitempty" doc:"Gmail search syntax (X-GM-RAW, or q for the Gmail API), ≤ 1024 characters; Gmail accounts only." example:"has:attachment newer_than:30d"`
	UseOutlookSearch bool        `json:"useOutlookSearch,omitempty" doc:"Use Microsoft Graph $search (Microsoft 365 accounts only)." example:"false"`
}

// HeaderCrit is a HEADER name/value search criterion.
type HeaderCrit struct {
	Name  string `json:"name" doc:"Header name, 1–64 characters." example:"X-Order-Id"`
	Value string `json:"value" doc:"Substring of the header value, ≤ 256 characters." example:"ORD-58213"`
}

// DraftInput is POST /drafts (structured or raw).
type DraftInput struct {
	From        *submit.AddressInput     `json:"from,omitempty" doc:"Sender; default the account address."`
	To          []submit.AddressInput    `json:"to,omitempty" doc:"To recipients (optional for a draft)."`
	Cc          []submit.AddressInput    `json:"cc,omitempty" doc:"Cc recipients."`
	Bcc         []submit.AddressInput    `json:"bcc,omitempty" doc:"Bcc recipients."`
	ReplyTo     []submit.AddressInput    `json:"replyTo,omitempty" doc:"Reply-To addresses."`
	Subject     string                   `json:"subject,omitempty" doc:"Subject." example:"Quote for the November order"`
	Text        string                   `json:"text,omitempty" doc:"Plain-text body." example:"Hello Marco,\n\nhere is the quote."`
	HTML        string                   `json:"html,omitempty" doc:"HTML body." example:"<p>Hello Marco,</p><p>here is the quote.</p>"`
	Headers     map[string]string        `json:"headers,omitempty" doc:"Extra headers (name to value)." example:"{\"X-Order-Id\":\"ORD-58213\"}"`
	MessageID   string                   `json:"messageId,omitempty" doc:"Message-ID to use (default: generated)." example:"<quote-58213@example.com>"`
	Attachments []submit.AttachmentInput `json:"attachments,omitempty" doc:"Attachments (base64 content)."`
	Raw         string                   `json:"raw,omitempty" doc:"Instead of the fields above: a complete RFC 822 message, base64, at most 25 MiB." example:"RnJvbTogU2FsZXMgPHNhbGVzQGV4YW1wbGUuY29tPg0KVG86IE1hcmNvIEJpYW5jaGkgPG1hcmNvLmJpYW5jaGlAZXhhbXBsZS5vcmc+DQpTdWJqZWN0OiBRdW90ZSBmb3IgdGhlIE5vdmVtYmVyIG9yZGVyDQoNCkhlbGxvIE1hcmNvLA0KDQpoZXJlIGlzIHRoZSBxdW90ZS4NCg=="`
}

// draftDoc is the Mongo drafts row.
type draftDoc struct {
	ID              string    `bson:"_id"`
	OrganizationID  string    `bson:"organizationId"`
	AccountID       string    `bson:"accountId"`
	Path            string    `bson:"path"`
	UID             uint32    `bson:"uid"`
	UIDValidity     uint32    `bson:"uidValidity"`
	MessageIDHeader string    `bson:"messageIdHeader,omitempty"`
	Subject         string    `bson:"subject,omitempty"`
	Size            int64     `bson:"size"`
	CreatedAt       time.Time `bson:"createdAt"`
	UpdatedAt       time.Time `bson:"updatedAt"`
}

// msgDoc is the indexed message fields we need.
type msgDoc struct {
	ID              string    `bson:"_id"`
	OrganizationID  string    `bson:"organizationId"`
	AccountID       string    `bson:"accountId"`
	MailboxID       string    `bson:"mailboxId"`
	UIDValidity     uint32    `bson:"uidValidity"`
	UID             uint32    `bson:"uid"`
	ModSeq          uint64    `bson:"modseq"`
	Flags           []string  `bson:"flags"`
	MessageIDHeader string    `bson:"messageIdHeader,omitempty"`
	EmailID         string    `bson:"emailId"`
	ThreadID        string    `bson:"threadId,omitempty"`
	InternalDate    time.Time `bson:"internalDate"`
	Size            int64     `bson:"size"`
	// Envelope summary written by the sync engine (P4.4).
	Subject string     `bson:"subject,omitempty"`
	From    *addrDoc   `bson:"from,omitempty"`
	To      []addrDoc  `bson:"to,omitempty"`
	Date    *time.Time `bson:"date,omitempty"`
}

type addrDoc struct {
	Name    string `bson:"name,omitempty"`
	Address string `bson:"address"`
}

// mbxDoc is the indexed mailbox fields we need.
type mbxDoc struct {
	ID             string     `bson:"_id"`
	OrganizationID string     `bson:"organizationId"`
	AccountID      string     `bson:"accountId"`
	Path           string     `bson:"path"`
	Delimiter      string     `bson:"delimiter,omitempty"`
	SpecialUse     string     `bson:"specialUse,omitempty"`
	UIDValidity    uint32     `bson:"uidValidity"`
	UIDNext        uint32     `bson:"uidNext"`
	HighestModSeq  uint64     `bson:"highestModseq"`
	Exists         uint32     `bson:"exists"`
	SyncMode       string     `bson:"syncMode,omitempty"`
	LastSyncAt     *time.Time `bson:"lastSyncAt,omitempty"`
	Status         string     `bson:"status"`
}
