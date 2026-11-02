// Package gmail is the Gmail API sync backend, Pub/Sub push handler and send path (P6.2).
package gmail

import (
	"context"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/events/types"
)

// Provider is accounts.provider for this backend.
const Provider = "gmail"

// Collection is gmailSyncState (P6.2).
const Collection = "gmailSyncState"

// SyncState is the per-account Gmail cursor. Per-message data lives on index.Message rows
// (providerId), never in this document — a large mailbox would exceed MongoDB's 16 MiB limit.
type SyncState struct {
	AccountID       string            `bson:"_id"`
	OrganizationID  string            `bson:"organizationId"`
	HistoryID       string            `bson:"historyId"`
	UIDValidity     uint32            `bson:"uidValidity"`
	WatchExpiration *time.Time        `bson:"watchExpiration,omitempty"`
	TopicName       string            `bson:"topicName,omitempty"`
	UpdatedAt       time.Time         `bson:"updatedAt"`
	NextUID         map[string]uint32 `bson:"nextUid,omitempty"`    // mailbox path → next uid
	MailboxIDs      map[string]string `bson:"mailboxIds,omitempty"` // path → mbx id
}

// Account is what the Gmail runner needs (mirror of sync.Account fields that apply).
type Account struct {
	ID             string
	OrganizationID string
	ExternalID     *string
	Email          string
	Paths          []string // ["*"] or system mailbox paths
	NotifyFrom     time.Time
	TopicName      string // Pub/Sub topic; empty → pull only

	AttachmentsS3, AttachmentsAV bool
	AuthResults                  string
	TrustedAuth                  []string
	DenyWebSafeHTML              bool
	DenyCalendarEvents           bool
	DenyReplyText                bool
	SyncFrom                     time.Time
	SpecialUse                   map[string]string
}

// Needs mirrors sync.Needs for optional payload parts.
type Needs struct {
	Headers        []string
	Text           bool
	TextMax        int64
	Attachments    bool
	AuthResults    string
	TrustedAuth    []string
	WebSafeHTML    bool
	CalendarEvents bool
	ReplyText      bool
}

// TokenSource returns a valid access token (oauth.Service.AccessToken or SA).
type TokenSource interface {
	AccessToken(ctx context.Context, accountID string) (token, username string, err error)
}

// DraftSink receives events the runner would emit (tests collect; production wraps events.Writer).
type DraftSink interface {
	Emit(ctx context.Context, organizationID, accountID string, typ, subject string, data any) error
}

// MemorySink collects drafts in memory.
type MemorySink struct {
	Mu     sync.Mutex
	Events []Emitted
}

// Emitted is one collected event.
type Emitted struct {
	Type, Subject string
	Data          any
}

// Emit implements DraftSink.
func (m *MemorySink) Emit(_ context.Context, _, _ string, typ, subject string, data any) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Events = append(m.Events, Emitted{Type: typ, Subject: subject, Data: data})
	return nil
}

// ReceivedOf returns message.received payloads.
func (m *MemorySink) ReceivedOf() []*types.MessageReceived {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var out []*types.MessageReceived
	for _, e := range m.Events {
		if d, ok := e.Data.(*types.MessageReceived); ok {
			out = append(out, d)
		}
	}
	return out
}
