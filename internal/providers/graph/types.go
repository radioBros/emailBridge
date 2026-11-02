// Package graph is the Microsoft Graph sync backend, change-notification handler and send path (P6.3).
package graph

import (
	"context"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/events/types"
)

// Provider is accounts.provider for this backend.
const Provider = "graph"

// Collection is graphSyncState (P6.3).
const Collection = "graphSyncState"

// SyncState is the per-account Graph cursor. Per-message maps are intentionally absent: those live on
// index.Message (providerId / flags) so a large mailbox cannot exceed MongoDB's 16 MiB document limit.
type SyncState struct {
	AccountID           string            `bson:"_id"`
	OrganizationID      string            `bson:"organizationId"`
	DeltaLinks          map[string]string `bson:"deltaLinks,omitempty"` // folderId → deltaLink
	UIDValidity         uint32            `bson:"uidValidity"`
	SubscriptionID      string            `bson:"subscriptionId,omitempty"`
	SubscriptionExpires *time.Time        `bson:"subscriptionExpires,omitempty"`
	ClientState         string            `bson:"clientState,omitempty"`
	UpdatedAt           time.Time         `bson:"updatedAt"`
	NextUID             map[string]uint32 `bson:"nextUid,omitempty"`    // mailbox path → next uid
	MailboxIDs          map[string]string `bson:"mailboxIds,omitempty"` // path → mbx id
	FolderIDs           map[string]string `bson:"folderIds,omitempty"`  // path → graph folder id
}

// Account is what the Graph runner needs.
type Account struct {
	ID              string
	OrganizationID  string
	ExternalID      *string
	Email           string
	UPN             string // userPrincipalName for app-only; empty → /me
	Paths           []string
	NotifyFrom      time.Time
	NotificationURL string // empty → pull only (no subscription create)

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

// TokenSource returns a valid access token (oauth.Service.AccessToken or client_credentials).
type TokenSource interface {
	AccessToken(ctx context.Context, accountID string) (token, username string, err error)
}

// StaticToken is a fixed token for tests.
type StaticToken struct {
	Token, Username string
}

// AccessToken implements TokenSource.
func (s StaticToken) AccessToken(context.Context, string) (string, string, error) {
	return s.Token, s.Username, nil
}

// DraftSink receives events the runner would emit.
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

// OfType returns events of a given type name.
func (m *MemorySink) OfType(typ string) []Emitted {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var out []Emitted
	for _, e := range m.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}
