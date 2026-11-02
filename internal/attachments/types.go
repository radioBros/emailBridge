package attachments

import (
	"context"
	"io"
	"time"
)

// Scan outcomes recorded on an attachment and on message.received (P4.3).
const (
	ScanClean    = "clean"
	ScanInfected = "infected"
	ScanError    = "error"
	ScanSkipped  = "skipped"
)

// Collection is the MongoDB collection name.
const Collection = "attachments"

// Doc is one attachment index row.
type Doc struct {
	ID             string    `bson:"_id" json:"id"`
	OrganizationID string    `bson:"organizationId" json:"organizationId"`
	AccountID      string    `bson:"accountId" json:"accountId"`
	MessageID      string    `bson:"messageId" json:"messageId"`
	PartID         string    `bson:"partId" json:"partId"`
	Filename       string    `bson:"filename,omitempty" json:"filename,omitempty"`
	ContentType    string    `bson:"contentType" json:"contentType"`
	Size           int64     `bson:"size" json:"size"`
	ContentID      string    `bson:"contentId,omitempty" json:"contentId,omitempty"`
	Disposition    string    `bson:"disposition,omitempty" json:"disposition,omitempty"`
	ObjectKey      string    `bson:"objectKey,omitempty" json:"objectKey,omitempty"`
	ETag           string    `bson:"etag,omitempty" json:"etag,omitempty"`
	Scan           string    `bson:"scan,omitempty" json:"scan,omitempty"`
	Infected       bool      `bson:"infected,omitempty" json:"infected,omitempty"`
	CreatedAt      time.Time `bson:"createdAt" json:"createdAt"`
}

// PartInfo is metadata returned with a Fetcher body.
type PartInfo struct {
	Filename    string
	ContentType string
	Size        int64
	ContentID   string
	Disposition string
}

// Fetcher loads one decoded MIME part over on-demand IMAP (W5 implements; tests use a fake).
type Fetcher interface {
	FetchPart(ctx context.Context, accountID, messageID, partID string) (io.ReadCloser, PartInfo, error)
}

// RawSource opens the message RFC822 (archive / test fakes).
type RawSource interface {
	OpenRaw(ctx context.Context, accountID, messageID string) (io.ReadCloser, error)
}

// Result is one attachment after Ingest (event / API enrichment).
type Result struct {
	ID          string `json:"id"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	ContentID   string `json:"contentId,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	PartID      string `json:"partId"`
	URL         string `json:"url,omitempty"`
	Scan        string `json:"scan,omitempty"`
}

// IngestInput is the sync hook for message.received.
type IngestInput struct {
	OrganizationID string
	AccountID      string
	MessageID      string
	Raw            []byte
	StoreS3        bool // effective attachments.s3
	Scan           bool // effective attachments.antivirus
}

// Meta is BODYSTRUCTURE / MIME metadata for one attachment leaf (Register path).
type Meta struct {
	PartID      string
	Filename    string
	ContentType string
	Size        int64
	ContentID   string
	Disposition string
}

// RegisterInput upserts attachment rows from already-parsed metas (no raw body).
type RegisterInput struct {
	OrganizationID string
	AccountID      string
	MessageID      string
	Metas          []Meta
}

// SignedAttachmentURL is the JSON response for ?url=signed.
type SignedAttachmentURL struct {
	URL       string    `json:"url" doc:"Public download URL (/a/{token}); anyone holding it can download until expiresAt." example:"https://emailbridge.example.com/a/djF8MTc5MzA5ODgwMHxvcmdfMDFtNWNhcXBrMGthc3Q2M3ljMTN3cHkyZDl8YXR0XzAxbTVjbmFlZTBwM2FxajczZDNuNmVqMzhw.xTuEv7r9QFvEX0W1pZk8LUgxevLClt4aZPXrV6cI11Q"`
	ExpiresAt time.Time `json:"expiresAt" doc:"When the URL stops working (attachments.signedUrlTtl after issue, at most 24 hours)." example:"2026-10-27T11:00:00Z"`
}
