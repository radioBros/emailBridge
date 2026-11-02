// Package mailbox holds the on-demand mailbox provider seam (W50 / G9).
package mailbox

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// SearchQuery is the normalised search criteria (P4.1 + W50 G28).
type SearchQuery struct {
	Text, Subject, From, To string
	Seen, Unseen, Flagged   *bool
	Since, Before           *time.Time
	HeaderName, HeaderValue string
	Larger, Smaller         *int64
	UIDMin, UIDMax          *uint32
	Labels                  []string
	GmailRaw                string
	UseOutlookSearch        bool
}

// FlagUpdate is a flags / labels change.
type FlagUpdate struct {
	Add, Remove, Set []string
}

// UploadInput is APPEND / insert.
type UploadInput struct {
	MailboxPath  string
	Raw          []byte
	Flags        []string
	InternalDate time.Time
}

// UploadResult is the provider-side identity after upload.
type UploadResult struct {
	ProviderID  string
	UID         uint32
	UIDValidity uint32
	Path        string
}

// Ops is the provider-agnostic mailbox operation surface used by the messages API.
type Ops interface {
	FetchRaw(ctx context.Context, providerMsgID string, maxBytes int64) ([]byte, error)
	Search(ctx context.Context, mailboxPath string, q SearchQuery) (providerIDs []string, uids []uint32, err error)
	SetFlags(ctx context.Context, providerMsgID string, uid uint32, mailboxPath string, uidValidity uint32, u FlagUpdate) error
	Move(ctx context.Context, providerMsgID string, uid uint32, srcPath string, uidValidity uint32, destPath string) error
	Delete(ctx context.Context, providerMsgID string, uid uint32, mailboxPath string, uidValidity uint32) error
	Upload(ctx context.Context, in UploadInput) (*UploadResult, error)
	CreateMailbox(ctx context.Context, path string, subscribe bool) error
	RenameMailbox(ctx context.Context, from, to string) error
	DeleteMailbox(ctx context.Context, path string) error
	Subscribe(ctx context.Context, path string, on bool) error
	// CategoriesAndLabels returns Gmail categories (CATEGORY_*) and user labels for a message, when known.
	CategoriesAndLabels(ctx context.Context, providerMsgID string) (categories, labels []string, err error)
}

// Unsupported returns the standard W50 problem for an unavailable op/criterion.
func Unsupported(detail string) error {
	return errs.New(errs.CodeUnsupportedForProvider, detail)
}
