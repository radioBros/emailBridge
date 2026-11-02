// Package mime builds and parses RFC 822 / MIME messages (S6 §2, S5 §4.1, S3 §3.5).
package mime

import (
	"io"
	"time"
)

// Address is a mailbox address with an optional display name.
type Address struct {
	Name    string
	Address string
}

// Attachment is an outbound attachment or inline part.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
	Disposition string // "inline" or "attachment"
	ContentID   string // without or with angle brackets; set for inline
}

// Message mirrors the structured submit body (S2 §4.3).
type Message struct {
	From        []Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     []Address
	Subject     string
	Text        string
	HTML        string
	Headers     map[string]string
	MessageID   string // kept byte-for-byte when set
	Date        time.Time
	Attachments []Attachment
}

// Envelope is the SMTP envelope (Bcc only here, never in transmitted headers).
type Envelope struct {
	From       string
	Recipients []string
}

// Built is the result of Build / BuildRaw.
type Built struct {
	Raw       []byte
	MessageID string
	Envelope  Envelope
	// HeaderTo and HeaderCc are the visible recipients of a raw message (its To / Cc headers). Envelope
	// recipients not among them are blind (Bcc) recipients.
	HeaderTo, HeaderCc []string
}

// Header maps lowercased header names to all values (S3 §3.5).
type Header map[string][]string

// AttachmentMeta is inbound attachment metadata (S3 §3.5).
type AttachmentMeta struct {
	Filename    string
	ContentType string
	Size        int64
	ContentID   string
	Disposition string
	PartID      string
}

// Options injects determinism for tests (boundary generator and clock).
type Options struct {
	Now      func() time.Time
	Boundary func() string
	Entropy  io.Reader // ULID entropy; nil → ulid.DefaultEntropy()
}

// DefaultTextMax is the default cap for TextParts (EB_SYNC_TEXT_MAX = 256KiB).
const DefaultTextMax = 256 << 10
