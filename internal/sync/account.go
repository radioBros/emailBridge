// Package sync is the inbound sync engine (S5): placement and leases of accounts over the sync nodes, one
// goroutine tree per owned account (connect, initial and periodic passes, IDLE), change detection per
// folder with the no-replay rule, fenced index writes and the events they produce.
package sync

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// Account states (S1 accounts.state, S5 §2).
const (
	StateNew          = "new"
	StateConnecting   = "connecting"
	StateSyncing      = "syncing"
	StateConnected    = "connected"
	StateAuthError    = "authError"
	StateConnectError = "connectError"
	StateDisconnected = "disconnected"
	StateDisabled     = "disabled"
	StateDeleting     = "deleting"
)

// Account is what the engine needs to run one account (credentials decrypted by the accounts package).
type Account struct {
	ID             string
	OrganizationID string
	ExternalID     *string
	Email          string
	State          string

	IMAPAddr           string // host:port
	IMAPTLS            imapc.TLSMode
	IMAPUser, IMAPPass string
	// DelegatedUser is the shared / delegated Microsoft 365 mailbox this account addresses through another
	// account's OAuth link (W54, G24); empty = the account's own mailbox.
	DelegatedUser string
	// Provider is accounts.provider: "" / "imap", "gmail" (Gmail API), "graph" (Microsoft Graph). P6.
	Provider string
	// Token, when set, returns a fresh OAuth access token and login name (P6.1); every connection asks for
	// one, so a refreshed token is used on the next reconnect. Password is then not used.
	Token            func(ctx context.Context) (token, username string, err error)
	IgnoreCertErrors bool

	// Network dial options (W57): applied on every IMAP dial via the egress helper.
	Proxy          string
	LocalAddress   string   // singular; wins over pool
	LocalAddresses []string // IP pool candidates
	IMAPStrategy   string
	// IMAPID overlays the global EB_IMAP_ID_* map (non-empty keys only).
	IMAPID map[string]string
	// DisableRev2 opts out of IMAP4rev2 (never ENABLE; treat server as rev1).
	DisableRev2 bool

	Paths       []string // ["*"] = every folder except \All and \Flagged
	IdlePaths   []string // default ["INBOX"]
	NotifyFrom  time.Time
	ResyncDelay time.Duration // 0 = engine default
	MaxIdle     time.Duration // 0 = engine default

	AuthFailingSince *time.Time

	// ProtocolLogs is the account switch protocolLogs.enabled (S12 §7); RawDebugUntil, when in the future,
	// switches the transcript to raw debug (full content except credentials) until then.
	ProtocolLogs  bool
	RawDebugUntil *time.Time

	// ArchiveRaw is the switch inbound.archiveRaw (S9 §4): the raw source of every new message is stored
	// in S3 before its event is written (S5 §4.1).
	ArchiveRaw bool
	// QResync is the switch sync.qresync (S5 §4): use QRESYNC when the server offers it.
	QResync bool
	// BounceDetection is the switch inbound.bounceDetection (P5.4): classify bounces, complaints and
	// auto-replies among new messages.
	BounceDetection bool
	// SpamScoring is the switch integrations.rspamd (P8.3): score new inbound mail (message.received.spam).
	SpamScoring bool
	// PEC is the switch pec.enabled (P9): recognise PEC envelopes and receipts (S17 §4).
	PEC bool
	// PECEvidence is the switch pec.evidenceArchive (P9.2): archive receipts and envelopes with Object Lock.
	PECEvidence bool

	// AttachmentsS3 / AttachmentsAV are attachments.s3 / attachments.antivirus (P4.3 / W48).
	AttachmentsS3 bool
	AttachmentsAV bool
	// AuthResults is "" (off), "header", or "verify" from inbound.authResults + authResults.mode (W48).
	AuthResults string
	// TrustedAuth is authResults.trustedAuthservIds for the organisation.
	TrustedAuth []string
	// DenyWebSafeHTML / DenyCalendarEvents / DenyReplyText are set when the organisation / account allow switch
	// (G31, default on) is off: the zero value allows, so an Account built without the switches keeps the fields.
	DenyWebSafeHTML    bool
	DenyCalendarEvents bool
	DenyReplyText      bool
	// SyncFrom limits the initial baseline UID SEARCH SINCE window (G16); zero = no limit.
	SyncFrom time.Time
	// SpecialUse maps role (sent|trash|junk|drafts|archive) → folder path overrides (G16).
	SpecialUse map[string]string
}

// Archiver stores raw inbound messages (S1 §7 key archive/<orgId>/<accountId>/<yyyy>/<mm>/<msgId>.eml).
type Archiver interface {
	PutRaw(ctx context.Context, organizationID, accountID, messageID string, internalDate time.Time, raw []byte) (key string, err error)
}

// Command is a sync command (S4 §6, S5 §6).
type Command struct {
	Cmd  string         `json:"cmd"` // resync | reconnect | flush | configChanged | appendSent | handoffReady
	Args map[string]any `json:"args,omitempty"`
	At   time.Time      `json:"at"`
}

// full reports whether a resync command asks for the full diff: mode "full" (or no mode); mode "fast" runs
// the normal STATUS-first pass (S2 syncAccount).
func (c Command) full() bool {
	m, _ := c.Args["mode"].(string)
	return m != "fast"
}

// Commands understood by the engine.
const (
	CmdResync        = "resync"
	CmdReconnect     = "reconnect"
	CmdFlush         = "flush"
	CmdConfigChanged = "configChanged"
)

// Accounts is the part of the accounts package the engine uses.
type Accounts interface {
	// Placeable lists the accounts that should be connected somewhere (not disabled, not deleting).
	Placeable(ctx context.Context) ([]string, error)
	// Load returns the account with decrypted credentials.
	Load(ctx context.Context, accountID string) (*Account, error)
	// Claimed records accounts.owner = {nodeID, epoch, since} after a lease claim and returns (and clears)
	// the pending commands stored while the account had no owner.
	Claimed(ctx context.Context, accountID, nodeID string, epoch uint64) ([]Command, error)
	// Transition moves the account to state `to` (fenced by epoch) and emits account.state_changed in the
	// same transaction. A no-op when the account already is in `to`.
	Transition(ctx context.Context, accountID string, epoch uint64, to string, reason *types.Reason) error
}

// PayloadNeeds says which optional parts message.received must carry for an organisation (S7 §4: the
// union over its enabled endpoints and routes).
type PayloadNeeds interface {
	Needs(ctx context.Context, organizationID string) (Needs, error)
}

// Needs is the optional payload of message.received.
type Needs struct {
	Headers     []string // lower-case names; "*" = all
	Text        bool
	TextMax     int64
	Attachments bool
	AuthResults string // "" | header | verify
	TrustedAuth []string
	// WebSafeHTML and CalendarEvents (P4): fetched from the same capped raw source as Text.
	WebSafeHTML    bool
	CalendarEvents bool
	// ReplyText (P4.2 / W48): extracted new reply body.
	ReplyText bool
}

// AttachmentIngest is the sync-side attachments hook (lazy-wired; attachments module starts after sync).
type AttachmentIngest interface {
	Register(ctx context.Context, organizationID, accountID, messageID string, metas []AttachmentMeta) ([]AttachmentResult, error)
	Ingest(ctx context.Context, organizationID, accountID, messageID string, raw []byte, storeS3, scan bool) ([]AttachmentResult, error)
	EraseMessage(ctx context.Context, accountID, messageID string) error
}

// AttachmentMeta is BODYSTRUCTURE metadata for Register.
type AttachmentMeta struct {
	PartID, Filename, ContentType, ContentID, Disposition string
	Size                                                  int64
}

// AttachmentResult enriches event attachments after Register/Ingest.
type AttachmentResult struct {
	ID, PartID, URL, Scan string
}

// NoNeeds is the default PayloadNeeds (no optional parts) until the webhook package provides one.
type NoNeeds struct{}

// Needs implements PayloadNeeds.
func (NoNeeds) Needs(context.Context, string) (Needs, error) { return Needs{}, nil }

// AuthOptions fills the IMAP credentials of o for acc: the OAuth token when the account uses OAuth, else the
// stored password.
func AuthOptions(ctx context.Context, acc *Account, o *imapc.Options) error {
	if acc.Token == nil {
		o.Username, o.Password = acc.IMAPUser, acc.IMAPPass
		return nil
	}
	tok, user, err := acc.Token(ctx)
	if err != nil {
		return err
	}
	o.Username, o.Password, o.AccessToken = acc.IMAPUser, "", tok
	if user != "" {
		o.Username = user
	}
	return nil
}

// PanicReporter receives recovered panics (implemented by *observability.Reporter).
type PanicReporter interface {
	CapturePanic(ctx context.Context, recovered any, stack []byte, tags map[string]string)
}
