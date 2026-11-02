// Package exports implements per-account bulk mailbox export (P7.2): NDJSON, gzip, AES-GCM
// encrypted at rest, resumable via checkpoint, signed download, retention and erasure hooks.
package exports

import (
	"encoding/json"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Collection is the MongoDB collection (S1 `exports`).
const Collection = "exports"

// JobKind is the jobs runner kind.
const JobKind = "account.export"

// Feature switch name (already in features catalogue).
const SwitchEnabled = "export.enabled"

// States.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateExpired   = "expired"
)

const (
	defaultRetention = 7 * 24 * time.Hour
	defaultBatch     = 50
	maxConcurrent    = 3
	magic            = "EBX1"
	defaultListLimit = 50
	maxListLimit     = 200
)

// Include selects what each NDJSON line contains.
type Include struct {
	Raw          bool `json:"raw,omitempty" bson:"raw,omitempty" doc:"Add the raw RFC 822 source (base64) to every line." example:"false"`
	Attachments  bool `json:"attachments,omitempty" bson:"attachments,omitempty" doc:"Add the attachments (metadata and base64 data) to every line." example:"false"`
	MetadataOnly bool `json:"metadataOnly,omitempty" bson:"metadataOnly,omitempty" doc:"Only index metadata (the default when nothing is selected); cannot be combined with raw or attachments." example:"true"`
}

// Filters bound the export scan.
type Filters struct {
	FolderIDs []string   `json:"folderIds,omitempty" bson:"folderIds,omitempty" doc:"Only these mailboxes (mbx_…); all folders when empty." example:"[\"mbx_01m5cez6h04ejt72gcx84wfqjf\"]"`
	Since     *time.Time `json:"since,omitempty" bson:"since,omitempty" doc:"Only messages received at or after this time." example:"2026-10-01T00:00:00Z"`
	Before    *time.Time `json:"before,omitempty" bson:"before,omitempty" doc:"Only messages received before this time." example:"2026-11-01T00:00:00Z"`
	Include   Include    `json:"include" bson:"include" doc:"What each line contains."`
}

// Checkpoint is the last fully written (mailboxId, uid).
type Checkpoint struct {
	MailboxID string `json:"mailboxId" bson:"mailboxId" doc:"Mailbox of the last fully written message." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	UID       uint32 `json:"uid" bson:"uid" doc:"UID of the last fully written message." example:"4811"`
}

// Progress of a running export.
type Progress struct {
	Done       int64       `json:"done" bson:"done" doc:"Messages written so far." example:"1200"`
	Total      int64       `json:"total" bson:"total" doc:"Messages to write (known once the scan started)." example:"4795"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty" bson:"checkpoint,omitempty" doc:"Resume point after a restart."`
	// NextPart is the 1-based index of the next part object to write (multipart resume).
	NextPart int `json:"nextPart,omitempty" bson:"nextPart,omitempty" doc:"1-based index of the next part file to write (internal resume state)." example:"3"`
	// PartCount is how many part objects are committed (API-visible).
	PartCount int `json:"partCount,omitempty" bson:"partCount,omitempty" doc:"Committed part files of the export." example:"2"`
}

// Export is the API / stored document (file key never returned in JSON).
type Export struct {
	ID             string                 `json:"id" bson:"_id" doc:"Export ID (exp_…)." example:"exp_01m5dckp30rpt94yha22n10ktn"`
	OrganizationID string                 `json:"organizationId" bson:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      string                 `json:"accountId" bson:"accountId" doc:"Exported account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	State          string                 `json:"state" bson:"state" doc:"queued, running, succeeded, failed, cancelled or expired." example:"succeeded"`
	JobID          string                 `json:"jobId,omitempty" bson:"jobId,omitempty" doc:"Background job writing the export (job_…)." example:"job_01m5dckp30v2y7c9k1d4r8e6wq"`
	Filters        Filters                `json:"filters" bson:"filters" doc:"The filters the export was started with."`
	Progress       *Progress              `json:"progress,omitempty" bson:"progress,omitempty" doc:"Progress of the job."`
	ObjectRef      string                 `json:"-" bson:"objectRef,omitempty"` // legacy single-blob key; unused for multipart
	Parts          []string               `json:"-" bson:"parts,omitempty"`     // S3 keys of committed encrypted gzip parts
	Encryption     crypto.EncryptedString `json:"-" bson:"encryption,omitempty"`
	MessageCount   int64                  `json:"messageCount,omitempty" bson:"messageCount,omitempty" doc:"Messages in the finished export." example:"4795"`
	ByteSize       int64                  `json:"byteSize,omitempty" bson:"byteSize,omitempty" doc:"Size of the finished export (compressed) in bytes." example:"1873402"`
	Error          *errs.Problem          `json:"error,omitempty" bson:"-" doc:"Why the export failed (RFC 9457 problem), when state is failed."`
	ErrorText      string                 `json:"-" bson:"error,omitempty"`
	CreatedBy      string                 `json:"createdBy,omitempty" bson:"createdBy,omitempty" doc:"User or API key that started the export." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt      time.Time              `json:"createdAt" bson:"createdAt" doc:"When the export was requested." example:"2026-10-27T10:15:00Z"`
	StartedAt      *time.Time             `json:"startedAt,omitempty" bson:"startedAt,omitempty" doc:"When the job started writing." example:"2026-10-27T10:15:02Z"`
	FinishedAt     *time.Time             `json:"finishedAt,omitempty" bson:"finishedAt,omitempty" doc:"When the export reached a final state." example:"2026-10-27T10:18:47Z"`
	ExpireAt       *time.Time             `json:"expireAt,omitempty" bson:"expireAt,omitempty" doc:"When the finished file is deleted (7 days after finishing by default)." example:"2026-11-03T10:18:47Z"`
}

func (e *Export) decodeError() {
	if e.ErrorText == "" {
		return
	}
	var p errs.Problem
	if err := json.Unmarshal([]byte(e.ErrorText), &p); err == nil {
		e.Error = &p
	}
}

// CreateInput is the POST body.
type CreateInput struct {
	FolderIDs []string   `json:"folderIds,omitempty" doc:"Only these mailboxes (mbx_…); default all folders." example:"[\"mbx_01m5cez6h04ejt72gcx84wfqjf\"]"`
	Since     *time.Time `json:"since,omitempty" doc:"Only messages received at or after this time." example:"2026-10-01T00:00:00Z"`
	Before    *time.Time `json:"before,omitempty" doc:"Only messages received before this time (after since)." example:"2026-11-01T00:00:00Z"`
	Include   Include    `json:"include" doc:"What each line contains (default metadataOnly)."`
}

// ListFilter for GET /exports.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
}

// List is a page of exports (API convention: data + nextCursor).
type List struct {
	Data            []Export `json:"data" doc:"The page of exports."`
	NextCursor      *string  `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6ImV4cF8wMW01ZDhjNjUwNWoxMXd2ZzFlZ2c0ZWg3ZSIsImZpZWxkIjoiY3JlYXRlZEF0IiwiZGVzYyI6dHJ1ZX0"`
	Total           *int64   `json:"total,omitempty" doc:"Number of the account's exports, with includeTotal=true." example:"2"`
	TotalIsEstimate *bool    `json:"totalIsEstimate,omitempty" doc:"Always false: total is an exact count." example:"false"`
}

// Accepted is the 202 body.
type Accepted struct {
	Export Export `json:"export" doc:"The queued export."`
	Job    any    `json:"job" doc:"The background job (see GET /v1/jobs/{jobId}); null when no job runner is configured."`
}

// SignedExportURL is the JSON for ?url=signed.
type SignedExportURL struct {
	URL       string    `json:"url" doc:"Public download URL (/e/{token}); anyone holding it can download until expiresAt." example:"https://emailbridge.example.com/e/djF8MTc5MzA5OTcwMHxvcmdfMDFtNWNhcXBrMGthc3Q2M3ljMTN3cHkyZDl8ZXhwXzAxbTVkY2twMzBycHQ5NHloYTIybjEwa3Ru.rOkzz36fKj3uLEbDC2vxSnL0ZWQM0FlWAfqh5Pypky8"`
	ExpiresAt time.Time `json:"expiresAt" doc:"When the URL stops working." example:"2026-10-27T11:15:00Z"`
}

// Line is one NDJSON object.
type Line struct {
	ID              string           `json:"id"`
	MailboxID       string           `json:"mailboxId"`
	MailboxPath     string           `json:"mailboxPath,omitempty"`
	UID             uint32           `json:"uid"`
	UIDValidity     uint32           `json:"uidValidity"`
	Flags           []string         `json:"flags,omitempty"`
	MessageIDHeader string           `json:"messageIdHeader,omitempty"`
	EmailID         string           `json:"emailId,omitempty"`
	ThreadID        string           `json:"threadId,omitempty"`
	InternalDate    time.Time        `json:"internalDate"`
	Size            int64            `json:"size"`
	Raw             string           `json:"raw,omitempty"`
	Attachments     []LineAttachment `json:"attachments,omitempty"`
}

// UnmarshalJSON accepts both mailboxPath and the legacy MailboxPath tag.
func (l *Line) UnmarshalJSON(b []byte) error {
	type alias Line
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*l = Line(a)
	if l.MailboxPath != "" {
		return nil
	}
	var legacy struct {
		MailboxPath string `json:"MailboxPath"`
	}
	if err := json.Unmarshal(b, &legacy); err == nil && legacy.MailboxPath != "" {
		l.MailboxPath = legacy.MailboxPath
	}
	return nil
}

// LineAttachment is attachment metadata (+ optional data) in an export line.
type LineAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size"`
	Data        string `json:"data,omitempty"`
}
