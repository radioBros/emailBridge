// Package accounts owns mail accounts as an API resource (S1 §3, S2 §4.2, S5 §2, S9 §4, S11 §5.1).
package accounts

import (
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collection is the MongoDB collection name (S1).
const Collection = "accounts"

// Account connection states (S1, S5 §2).
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

// AllStates lists every valid account state.
var AllStates = []string{
	StateNew, StateConnecting, StateSyncing, StateConnected,
	StateAuthError, StateConnectError, StateDisconnected, StateDisabled, StateDeleting,
}

// TLS modes (S1 accounts.imap.tls / smtp.tls).
const (
	TLSImplicit = "implicit"
	TLSStartTLS = "starttls"
	TLSNone     = "none"
)

// SMTP send policies (S1).
const (
	PolicyStandard   = "standard"
	PolicyAtMostOnce = "atMostOnce"
)

// Copy-to-Sent modes (S1 / S6 §5).
const (
	CopyToSentAuto   = "auto"
	CopyToSentAlways = "always"
	CopyToSentNever  = "never"
)

// Sync command names stored in pendingCommands (S4 §6). Match ebsync.Cmd*.
const (
	CmdResync        = "resync"
	CmdReconnect     = "reconnect"
	CmdFlush         = "flush"
	CmdConfigChanged = "configChanged"
)

// Account is the stored document (S1 accounts). Passwords are encrypted; API responses use View().
type Account struct {
	ID               string       `bson:"_id" json:"id"`
	OrganizationID   string       `bson:"organizationId" json:"organizationId"`
	ExternalID       *string      `bson:"externalId,omitempty" json:"externalId,omitempty"`
	Name             string       `bson:"name" json:"name"`
	Email            string       `bson:"email" json:"email"`
	State            string       `bson:"state" json:"state"`
	StateReason      *StateReason `bson:"stateReason,omitempty" json:"stateReason,omitempty"`
	AuthFailingSince *time.Time   `bson:"authFailingSince,omitempty" json:"authFailingSince,omitempty"`
	IMAP             IMAPConfig   `bson:"imap" json:"imap"`
	SMTP             SMTPConfig   `bson:"smtp" json:"smtp"`
	Sync             SyncConfig   `bson:"sync" json:"sync"`
	TZ               string       `bson:"tz,omitempty" json:"tz,omitempty"`
	Locale           string       `bson:"locale,omitempty" json:"locale,omitempty"`
	CopyToSent       string       `bson:"copyToSent" json:"-"`
	ArchiveRetention string       `bson:"archiveRetention,omitempty" json:"-"`
	// Network is optional dialer options (proxy / localAddress / ehloName) — P5.3.
	Network  *NetworkConfig `bson:"network,omitempty" json:"network,omitempty"`
	Switches map[string]any `bson:"switches,omitempty" json:"switches,omitempty"`
	// Metadata is the caller's own free-form object (G4): ≤ 32 keys, scalar values, ≤ 8 KB as JSON.
	Metadata map[string]any `bson:"metadata,omitempty" json:"metadata,omitempty"`
	// RawDebugUntil is set when protocolLogs.rawDebug is enabled; sync treats raw mode as on while it is
	// in the future (S12 §7). Expiry turns the mode off without a further write.
	RawDebugUntil *time.Time `bson:"rawDebugUntil,omitempty" json:"-"`
	Owner         *Owner     `bson:"owner,omitempty" json:"-"`
	// OAuth is written by internal/oauth (P6.1); accounts only reads whether it is set.
	OAuth *OAuthLink `bson:"oauth,omitempty" json:"-"`
	// Identity is the pinned authenticated mailbox identity (P6.4).
	Identity *IdentityPin `bson:"identity,omitempty" json:"identity,omitempty"`
	// Provider is how the mailbox is reached: "imap" (default), "gmail" (Gmail API) or "graph" (Microsoft Graph), P6.
	Provider string `bson:"provider,omitempty" json:"provider,omitempty"`
	// UseAuthServer means IMAP/SMTP passwords are fetched from the org credential server (W54).
	UseAuthServer bool `bson:"useAuthServer,omitempty" json:"-"`
	// Delegated authenticates through another same-org account's OAuth (Microsoft 365 shared mailbox, W54).
	Delegated       *DelegatedRef    `bson:"delegated,omitempty" json:"delegated,omitempty"`
	PendingCommands []PendingCommand `bson:"pendingCommands,omitempty" json:"-"`
	LastSyncAt      *time.Time       `bson:"lastSyncAt,omitempty" json:"lastSyncAt,omitempty"`
	LastErrorAt     *time.Time       `bson:"lastErrorAt,omitempty" json:"lastErrorAt,omitempty"`
	RecentErrors    []RecentError    `bson:"recentErrors,omitempty" json:"-"`
	Capabilities    map[string]any   `bson:"capabilities,omitempty" json:"-"`
	DeletedAt       *time.Time       `bson:"deletedAt,omitempty" json:"-"`
	CreatedAt       time.Time        `bson:"createdAt" json:"createdAt"`
	UpdatedAt       time.Time        `bson:"updatedAt" json:"updatedAt"`
	Version         int64            `bson:"version" json:"version"`
	CreatedBy       string           `bson:"createdBy,omitempty" json:"-"`
}

// StateReason is the last cause of a state change (redacted).
type StateReason struct {
	Kind    string    `bson:"kind" json:"kind" doc:"Error kind of the cause: auth, temporary, permanent, config or ok." example:"auth"`
	Code    string    `bson:"code" json:"code" doc:"Problem code of the cause (e.g. mail_auth_failed, mail_connect_failed, disabled, resume)." example:"mail_auth_failed"`
	Message string    `bson:"message,omitempty" json:"message,omitempty" doc:"Short human-readable cause; server text is redacted." example:"IMAP LOGIN rejected: [AUTHENTICATIONFAILED] Invalid credentials"`
	At      time.Time `bson:"at" json:"at" doc:"When the state changed for this reason (RFC 3339 UTC)." example:"2026-10-26T08:14:03Z"`
}

// Owner is the sync lease holder (S4 §5 fencing epoch).
type Owner struct {
	NodeID string    `bson:"nodeId" json:"nodeId"`
	Epoch  uint64    `bson:"epoch" json:"epoch"`
	Since  time.Time `bson:"since" json:"since"`
}

// PendingCommand waits for the sync owner (S4 §6).
type PendingCommand struct {
	Cmd string         `bson:"cmd" json:"cmd"`
	At  time.Time      `bson:"at" json:"at"`
	By  string         `bson:"by,omitempty" json:"by,omitempty"`
	Arg map[string]any `bson:"arg,omitempty" json:"arg,omitempty"`
}

// RecentError is one entry of the account error timeline (S5 §7).
type RecentError struct {
	At       time.Time `bson:"at"`
	Kind     string    `bson:"kind"`
	Code     string    `bson:"code"`
	Protocol string    `bson:"protocol,omitempty"`
	Response string    `bson:"response,omitempty"`
	Hint     string    `bson:"hint,omitempty"`
}

// MailAuth is stored credentials (pass encrypted).
type MailAuth struct {
	User string                 `bson:"user" json:"user"`
	Pass crypto.EncryptedString `bson:"pass,omitempty" json:"-"`
}

// IMAPConfig is accounts.imap.
type IMAPConfig struct {
	Host         string   `bson:"host" json:"host"`
	Port         int      `bson:"port" json:"port"`
	TLS          string   `bson:"tls" json:"tls"`
	Auth         MailAuth `bson:"auth" json:"auth"`
	IDName       string   `bson:"idName,omitempty" json:"idName,omitempty"`
	IDVersion    string   `bson:"idVersion,omitempty" json:"idVersion,omitempty"`
	IDVendor     string   `bson:"idVendor,omitempty" json:"idVendor,omitempty"`
	IDSupportURL string   `bson:"idSupportUrl,omitempty" json:"idSupportUrl,omitempty"`
	DisableRev2  bool     `bson:"disableRev2,omitempty" json:"disableRev2,omitempty"`
}

// SMTPConfig is accounts.smtp.
type SMTPConfig struct {
	Host        string   `bson:"host" json:"host"`
	Port        int      `bson:"port" json:"port"`
	TLS         string   `bson:"tls" json:"tls"`
	Auth        MailAuth `bson:"auth" json:"auth"`
	Policy      string   `bson:"policy,omitempty" json:"policy,omitempty"`
	MaxAttempts *int     `bson:"maxAttempts,omitempty" json:"maxAttempts,omitempty"`
	BackoffBase string   `bson:"backoffBase,omitempty" json:"backoffBase,omitempty"`
	// GatewayID sends via an organisation SMTP gateway instead of host/auth (P5.3).
	GatewayID string `bson:"gatewayId,omitempty" json:"gatewayId,omitempty"`
}

// NetworkConfig is accounts.network (P5.3 / W57).
type NetworkConfig struct {
	Proxy          string   `bson:"proxy,omitempty" json:"proxy,omitempty" doc:"Outbound proxy for IMAP and SMTP connections: socks5, socks5h, http or https URL. Credentials in the URL are stored as given; prefer a proxy that authenticates by source IP." example:"socks5://proxy.example.net:1080"`
	LocalAddress   string   `bson:"localAddress,omitempty" json:"localAddress,omitempty" doc:"Local IP address to bind outgoing connections to (must exist on the host)." example:"192.0.2.10"`
	EHLOName       string   `bson:"ehloName,omitempty" json:"ehloName,omitempty" doc:"Host name sent in SMTP EHLO (≤ 255 characters). Default: the instance host name." example:"mail-out.example.com"`
	LocalAddresses []string `bson:"localAddresses,omitempty" json:"localAddresses,omitempty" doc:"Pool of local IP addresses; the strategies below pick one per connection." example:"[\"192.0.2.10\",\"192.0.2.11\"]"`
	SMTPStrategy   string   `bson:"smtpStrategy,omitempty" json:"smtpStrategy,omitempty" doc:"How SMTP picks from localAddresses: random, round-robin or hash-by-account." example:"round-robin"`
	IMAPStrategy   string   `bson:"imapStrategy,omitempty" json:"imapStrategy,omitempty" doc:"How IMAP picks from localAddresses: random, round-robin or hash-by-account." example:"hash-by-account"`
}

// SyncConfig is accounts.sync.
type SyncConfig struct {
	Paths       []string          `bson:"paths,omitempty" json:"paths,omitempty" doc:"Mailbox paths to synchronise; * means every mailbox. Default [\"*\"]." example:"[\"INBOX\",\"Sent\",\"Archive/*\"]"`
	IdlePaths   []string          `bson:"idlePaths,omitempty" json:"idlePaths,omitempty" doc:"Mailboxes kept open with IMAP IDLE for push-speed change detection; the others are polled. Default [\"INBOX\"]." example:"[\"INBOX\"]"`
	NotifyFrom  *time.Time        `bson:"notifyFrom,omitempty" json:"notifyFrom,omitempty" doc:"Only messages received after this time raise message.received events; older ones are indexed silently." example:"2026-10-01T00:00:00Z"`
	SyncFrom    *time.Time        `bson:"syncFrom,omitempty" json:"syncFrom,omitempty" doc:"Ignore messages older than this time completely (not indexed)." example:"2026-01-01T00:00:00Z"`
	ResyncDelay string            `bson:"resyncDelay,omitempty" json:"resyncDelay,omitempty" doc:"Interval between polls of mailboxes that are not watched with IDLE (Go duration)." example:"5m"`
	MaxIdle     string            `bson:"maxIdle,omitempty" json:"maxIdle,omitempty" doc:"How long one IDLE command may run before it is renewed (Go duration)." example:"25m"`
	SpecialUse  map[string]string `bson:"specialUse,omitempty" json:"specialUse,omitempty" doc:"Overrides for special-use folder detection: role (sent, drafts, trash, junk, archive) → mailbox path." example:"{\"sent\":\"Posta inviata\",\"trash\":\"Cestino\"}"`
}

// MailAuthInput is write-side auth (plaintext pass).
type MailAuthInput struct {
	User string `json:"user" doc:"Login user name, usually the full email address." example:"reception@example.com"`
	Pass string `json:"pass,omitempty" doc:"Password or app password. Write-only: stored encrypted, never returned (responses show auth.hasPassword). Not needed with OAuth, delegated access or the credential server." example:"app-password-from-provider"`
}

// IMAPConfigInput is create/patch/verify IMAP settings.
type IMAPConfigInput struct {
	Host         string        `json:"host" doc:"IMAP server host name." example:"imap.example.com"`
	Port         int           `json:"port" doc:"IMAP server port (1–65535); usually 993 with implicit TLS, 143 with starttls." example:"993"`
	TLS          string        `json:"tls" doc:"Transport security: implicit (TLS from the first byte), starttls or none." example:"implicit"`
	Auth         MailAuthInput `json:"auth" doc:"IMAP login."`
	IDName       string        `json:"idName,omitempty" doc:"Client name sent in the IMAP ID command (≤ 128 characters); overrides the instance default." example:"EmailBridge"`
	IDVersion    string        `json:"idVersion,omitempty" doc:"Client version sent in IMAP ID (≤ 64 characters)." example:"1.4.0"`
	IDVendor     string        `json:"idVendor,omitempty" doc:"Vendor sent in IMAP ID (≤ 128 characters)." example:"Example S.r.l."`
	IDSupportURL string        `json:"idSupportUrl,omitempty" doc:"Support URL sent in IMAP ID (≤ 512 characters)." example:"https://support.example.com"`
	DisableRev2  bool          `json:"disableRev2,omitempty" doc:"Do not enable IMAP4rev2 even when the server offers it (workaround for broken servers)." example:"false"`
}

// SMTPConfigInput is create/patch/verify SMTP settings.
type SMTPConfigInput struct {
	Host        string        `json:"host" doc:"SMTP submission server host name." example:"smtp.example.com"`
	Port        int           `json:"port" doc:"SMTP port (1–65535); usually 465 with implicit TLS, 587 with starttls." example:"465"`
	TLS         string        `json:"tls" doc:"Transport security: implicit, starttls or none." example:"implicit"`
	Auth        MailAuthInput `json:"auth" doc:"SMTP login."`
	Policy      string        `json:"policy,omitempty" doc:"Retry policy: standard (retry temporary failures) or atMostOnce (never resend when the outcome is unknown). Default standard." example:"standard"`
	MaxAttempts *int          `json:"maxAttempts,omitempty" doc:"Maximum send attempts per message (≥ 1). Default: the instance setting." example:"5"`
	BackoffBase string        `json:"backoffBase,omitempty" doc:"Base delay of the exponential retry backoff (Go duration)." example:"30s"`
	GatewayID   string        `json:"gatewayId,omitempty" doc:"Send through this organisation SMTP gateway (gwy_…) instead of host/auth." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
}

// CreateInput is POST /v1/accounts.
type CreateInput struct {
	Name       string `json:"name" doc:"Display name (required, not blank)." example:"Reception"`
	Email      string `json:"email" doc:"Email address of the mailbox (required)." example:"reception@example.com"`
	ExternalID string `json:"externalId,omitempty" doc:"Your own reference for the account, unique in the organisation: 1–128 characters of [A-Za-z0-9._@+-]." example:"crm-48213"`
	// Provider is imap (default), gmail or graph (P6). Immutable after create.
	Provider   string          `json:"provider,omitempty" doc:"How the mailbox is reached: imap (default), gmail (Gmail API) or graph (Microsoft Graph). Immutable after create." example:"imap"`
	IMAP       IMAPConfigInput `json:"imap" doc:"IMAP server settings."`
	SMTP       SMTPConfigInput `json:"smtp" doc:"SMTP submission settings."`
	Sync       *SyncConfig     `json:"sync,omitempty" doc:"Synchronisation settings; omitted values use the defaults."`
	TZ         string          `json:"tz,omitempty" doc:"IANA time zone used for send windows and date rendering." example:"Europe/Rome"`
	Locale     string          `json:"locale,omitempty" doc:"Language of account-related texts; one of the supported UI languages." example:"it"`
	Network    *NetworkConfig  `json:"network,omitempty" doc:"Optional dialer settings (proxy, source address, EHLO name)."`
	Switches   map[string]any  `json:"switches,omitempty" doc:"Account-scoped feature switches (name → value); unknown or non-account switches are rejected." example:"{\"protocolLogs.enabled\":true,\"accounts.ignoreCertErrors\":false}"`
	Metadata   map[string]any  `json:"metadata,omitempty" doc:"Your own free-form data for the account (CRM IDs, tenant codes, tags): at most 32 keys of 1–64 characters [A-Za-z0-9_.-]; values are strings (≤ 1024 bytes), numbers, booleans or null; at most 8 KB as JSON. Available to rules as account.metadata." example:"{\"crmId\":\"48213\",\"tier\":2,\"vip\":true}"`
	SkipVerify bool            `json:"skipVerify,omitempty" doc:"Save without testing the IMAP/SMTP login first. Default false: the account is created only when both logins succeed." example:"false"`
	// OAuth links the account to an OAuth app without user consent (W43 SA / app-only).
	OAuth *OAuthBindInput `json:"oauth,omitempty" doc:"Bind the account to a service-account / app-only OAuth app (no user consent)."`
	// Auth selects external credential server (W54).
	Auth *AuthInput `json:"auth,omitempty" doc:"Credential source options."`
	// Delegated is a Microsoft 365 shared / delegated mailbox (W54).
	Delegated *DelegatedRef `json:"delegated,omitempty" doc:"Shared / delegated mailbox: authenticate through another account's OAuth."`
}

// AuthInput is create/patch auth options (W54).
type AuthInput struct {
	UseAuthServer bool `json:"useAuthServer,omitempty" doc:"Fetch IMAP/SMTP passwords from the organisation's external credential server on every login instead of storing them." example:"true"`
}

// DelegatedRef points at another account's OAuth for shared mailbox access (W54).
type DelegatedRef struct {
	Account string `bson:"account" json:"account" doc:"Account (acc_…, same organisation) whose OAuth token is used to open the mailbox." example:"acc_01m5x5dtxq189hak49x3b25a5h"`
	User    string `bson:"user" json:"user" doc:"Shared mailbox address to open with that token." example:"info@example.com"`
}

// OAuthBindInput is accounts.oauth on create/patch (W43).
type OAuthBindInput struct {
	AppID string `json:"appId" doc:"OAuth app (oap_…) of kind service account or app-only." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
}

// PatchInput is PATCH /v1/accounts/{accountId} (JSON Merge Patch).
type PatchInput struct {
	Name       *string `json:"name,omitempty"`
	Email      *string `json:"email,omitempty"`
	ExternalID *string `json:"externalId,omitempty"` // null clears; omit leaves
	ClearExt   bool    `json:"-"`                    // set by API when externalId is JSON null
	// Provider is rejected when set (immutable after create, P6).
	Provider *string          `json:"provider,omitempty"`
	IMAP     *IMAPConfigInput `json:"imap,omitempty"`
	SMTP     *SMTPConfigInput `json:"smtp,omitempty"`
	Sync     *SyncConfig      `json:"sync,omitempty"`
	TZ       *string          `json:"tz,omitempty"`
	Locale   *string          `json:"locale,omitempty"`
	Network  *NetworkConfig   `json:"network,omitempty"`
	Switches map[string]any   `json:"switches,omitempty"`
	Disabled *bool            `json:"disabled,omitempty"`
	// RePin accepts a new mailbox identity when IMAP/SMTP credentials change (P6.4).
	RePin bool `json:"rePin,omitempty"`
	// OAuth links without user consent (W43).
	OAuth *OAuthBindInput `json:"oauth,omitempty"`
	// Auth selects external credential server (W54).
	Auth *AuthInput `json:"auth,omitempty"`
	// Delegated updates shared-mailbox binding; null clears (W54).
	Delegated      *DelegatedRef `json:"delegated,omitempty"`
	ClearDelegated bool          `json:"-"` // set by API when delegated is JSON null
	// Metadata is a JSON Merge Patch of the stored metadata (G4): a key set to nil is removed; the merged
	// result is re-validated. ClearMetadata (metadata: null) removes every key.
	Metadata      map[string]any `json:"-"`
	ClearMetadata bool           `json:"-"`
}

// ListFilter is GET /v1/accounts.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	State        string
	Q            string
	// Metadata filters by metadata.<key> == value (string equality, at most 3, G4).
	Metadata map[string]string
}

// AccountList is the list response.
type AccountList struct {
	Data        []Account `json:"data"`
	NextCursor  *string   `json:"nextCursor"`
	Total       *int64    `json:"total,omitempty"`
	TotalCapped *bool     `json:"totalCapped,omitempty"`
}

// VerifyRequest is POST /v1/accounts/verify.
type VerifyRequest struct {
	IMAP             *IMAPConfigInput `json:"imap,omitempty" doc:"IMAP settings to test (the password is required here)."`
	SMTP             *SMTPConfigInput `json:"smtp,omitempty" doc:"SMTP settings to test (the password is required here)."`
	IgnoreCertErrors bool             `json:"ignoreCertErrors,omitempty" doc:"Accept an invalid or self-signed server certificate for this test (same effect as the accounts.ignoreCertErrors switch)." example:"false"` // accounts.ignoreCertErrors for unsaved settings
}

// VerifyProtocolResult is one protocol's verify outcome.
type VerifyProtocolResult struct {
	OK           bool              `json:"ok" doc:"True when the connection, TLS and login succeeded." example:"true"`
	LatencyMs    int               `json:"latencyMs,omitempty" doc:"Time the test took, in milliseconds." example:"412"`
	Capabilities []string          `json:"capabilities,omitempty" doc:"Capabilities the server announced (IMAP) or the relevant SMTP extensions it supports." example:"[\"IDLE\",\"IMAP4REV1\",\"MOVE\",\"QRESYNC\",\"UIDPLUS\"]"`
	SpecialUse   map[string]string `json:"specialUse,omitempty" doc:"IMAP only: detected special-use folders, role → mailbox path." example:"{\"sent\":\"Sent\",\"drafts\":\"Drafts\",\"trash\":\"Trash\",\"junk\":\"Spam\"}"`
	Code         string            `json:"code,omitempty" doc:"Problem code when ok is false: mail_connect_failed, mail_tls_failed, mail_auth_failed, mail_timeout or mail_protocol_error." example:"mail_auth_failed"`
	Detail       string            `json:"detail,omitempty" doc:"Server answer or error text when ok is false (≤ 512 characters; anything mentioning a password is redacted)." example:"535 5.7.8 Authentication credentials invalid"`
}

// VerifyResult is the verify response.
type VerifyResult struct {
	IMAP *VerifyProtocolResult `json:"imap,omitempty" doc:"IMAP outcome; present when imap was sent."`
	SMTP *VerifyProtocolResult `json:"smtp,omitempty" doc:"SMTP outcome; present when smtp was sent."`
}

// SyncInput is POST …/sync.
type SyncInput struct {
	Mode  string   `json:"mode,omitempty" enum:"fast,full" default:"fast" doc:"fast (STATUS-first) or full (every flag and UID diffed)" example:"full"`
	Paths []string `json:"paths,omitempty" doc:"Only resync these mailbox paths; omit for every synchronised mailbox." example:"[\"INBOX\",\"Sent\"]"`
}

// JobAccepted matches OpenAPI JobAccepted (wraps jobs.Job JSON).
type JobAccepted struct {
	Job any `json:"job" doc:"The queued job (see GET /v1/jobs/{jobId}); poll it for progress and the result." example:"{\"id\":\"job_01m5cnaee0w96fc46z350nc6q0\",\"organizationId\":\"org_01m5caqpk0kast63yc13wpy2d9\",\"kind\":\"account.resync\",\"state\":\"queued\",\"createdBy\":\"usr_01m5ccvej0dqefkn0xhkcq2q6z\",\"createdAt\":\"2026-10-27T09:30:00Z\"}"`
}

// MailAuthView is the API form of auth (no password).
type MailAuthView struct {
	User        string `json:"user" doc:"Login user name." example:"reception@example.com"`
	HasPassword bool   `json:"hasPassword" doc:"True when a password is stored (the password itself is never returned)." example:"true"`
}

// IMAPConfigView is IMAP without secrets.
type IMAPConfigView struct {
	Host         string       `json:"host" doc:"IMAP server host name." example:"imap.example.com"`
	Port         int          `json:"port" doc:"IMAP server port." example:"993"`
	TLS          string       `json:"tls" doc:"Transport security: implicit, starttls or none." example:"implicit"`
	Auth         MailAuthView `json:"auth" doc:"IMAP login (no password)."`
	IDName       string       `json:"idName,omitempty" doc:"Client name sent in IMAP ID." example:"EmailBridge"`
	IDVersion    string       `json:"idVersion,omitempty" doc:"Client version sent in IMAP ID." example:"1.4.0"`
	IDVendor     string       `json:"idVendor,omitempty" doc:"Vendor sent in IMAP ID." example:"Example S.r.l."`
	IDSupportURL string       `json:"idSupportUrl,omitempty" doc:"Support URL sent in IMAP ID." example:"https://support.example.com"`
	DisableRev2  bool         `json:"disableRev2,omitempty" doc:"IMAP4rev2 is not enabled for this account." example:"false"`
}

// SMTPConfigView is SMTP without secrets.
type SMTPConfigView struct {
	Host        string       `json:"host" doc:"SMTP server host name." example:"smtp.example.com"`
	Port        int          `json:"port" doc:"SMTP server port." example:"465"`
	TLS         string       `json:"tls" doc:"Transport security: implicit, starttls or none." example:"implicit"`
	Auth        MailAuthView `json:"auth" doc:"SMTP login (no password)."`
	Policy      string       `json:"policy,omitempty" doc:"Retry policy: standard or atMostOnce." example:"standard"`
	MaxAttempts *int         `json:"maxAttempts,omitempty" doc:"Maximum send attempts per message." example:"5"`
	BackoffBase string       `json:"backoffBase,omitempty" doc:"Base delay of the retry backoff (Go duration)." example:"30s"`
	GatewayID   string       `json:"gatewayId,omitempty" doc:"Organisation SMTP gateway used instead of host/auth." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
}

// AccountView is the API Account schema (credentials redacted).
type AccountView struct {
	ID               string         `json:"id" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	OrganizationID   string         `json:"organizationId" doc:"Organisation that owns the account." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	ExternalID       *string        `json:"externalId,omitempty" doc:"Your own reference, unique in the organisation." example:"crm-48213"`
	Name             string         `json:"name" doc:"Display name." example:"Reception"`
	Email            string         `json:"email" doc:"Email address of the mailbox." example:"reception@example.com"`
	State            string         `json:"state" doc:"Connection state: new, connecting, syncing, connected, authError, connectError, disconnected, disabled or deleting." example:"connected"`
	StateReason      *StateReason   `json:"stateReason,omitempty" doc:"Cause of the last state change."`
	AuthFailingSince *time.Time     `json:"authFailingSince,omitempty" doc:"Start of the current run of login failures; the account is disabled automatically when it lasts too long." example:"2026-10-26T08:14:03Z"`
	IMAP             IMAPConfigView `json:"imap" doc:"IMAP settings (no password)."`
	SMTP             SMTPConfigView `json:"smtp" doc:"SMTP settings (no password)."`
	Sync             SyncConfig     `json:"sync" doc:"Synchronisation settings."`
	TZ               string         `json:"tz,omitempty" doc:"IANA time zone." example:"Europe/Rome"`
	Locale           string         `json:"locale,omitempty" doc:"Language of account-related texts." example:"it"`
	Network          *NetworkConfig `json:"network,omitempty" doc:"Dialer settings, when set."`
	Switches         map[string]any `json:"switches,omitempty" doc:"Account-level feature switch overrides (name → value)." example:"{\"protocolLogs.enabled\":true}"`
	Metadata         map[string]any `json:"metadata,omitempty" doc:"Your own free-form data for the account (string, number, boolean or null values); omitted when empty." example:"{\"crmId\":\"48213\",\"tier\":2,\"vip\":true}"`
	Provider         string         `json:"provider,omitempty" doc:"imap, gmail or graph; absent means imap." example:"imap"`
	UseAuthServer    bool           `json:"useAuthServer,omitempty" doc:"Passwords come from the organisation's credential server." example:"false"`
	Delegated        *DelegatedRef  `json:"delegated,omitempty" doc:"Shared-mailbox binding, when set."`
	// Identity is the pinned mailbox identity (P6.4 §3); no secrets.
	Identity    *IdentityPin `json:"identity,omitempty" doc:"Mailbox identity pinned at the first successful login; a credential change that reaches another mailbox is refused (identity_mismatch)."`
	LastSyncAt  *time.Time   `json:"lastSyncAt,omitempty" doc:"End of the last completed synchronisation pass." example:"2026-10-27T09:12:44Z"`
	LastErrorAt *time.Time   `json:"lastErrorAt,omitempty" doc:"Time of the last connection or login error." example:"2026-10-26T08:14:03Z"`
	OwnerNode   string       `json:"ownerNode,omitempty" doc:"Cluster node that currently holds the account's sync lease." example:"eb-node-2"`
	CreatedAt   time.Time    `json:"createdAt" doc:"When the account was registered." example:"2026-10-20T10:02:11Z"`
	UpdatedAt   time.Time    `json:"updatedAt" doc:"Last change of the account." example:"2026-10-27T09:12:44Z"`
	Version     int64        `json:"version" doc:"Resource version; the ETag is this number in quotes." example:"3"`
}

// View returns the redacted API representation.
func (a Account) View() AccountView {
	v := AccountView{
		ID: a.ID, OrganizationID: a.OrganizationID, ExternalID: a.ExternalID,
		Name: a.Name, Email: a.Email, State: a.State, StateReason: a.StateReason,
		AuthFailingSince: a.AuthFailingSince, Provider: a.Provider, UseAuthServer: a.UseAuthServer,
		Delegated: a.Delegated, Identity: a.Identity,
		IMAP: IMAPConfigView{
			Host: a.IMAP.Host, Port: a.IMAP.Port, TLS: a.IMAP.TLS,
			IDName: a.IMAP.IDName, IDVersion: a.IMAP.IDVersion, IDVendor: a.IMAP.IDVendor,
			IDSupportURL: a.IMAP.IDSupportURL, DisableRev2: a.IMAP.DisableRev2,
			Auth: MailAuthView{User: a.IMAP.Auth.User, HasPassword: !a.IMAP.Auth.Pass.IsZero()},
		},
		SMTP: SMTPConfigView{
			Host: a.SMTP.Host, Port: a.SMTP.Port, TLS: a.SMTP.TLS, Policy: a.SMTP.Policy,
			MaxAttempts: a.SMTP.MaxAttempts, BackoffBase: a.SMTP.BackoffBase, GatewayID: a.SMTP.GatewayID,
			Auth: MailAuthView{User: a.SMTP.Auth.User, HasPassword: !a.SMTP.Auth.Pass.IsZero()},
		},
		Sync: a.Sync, TZ: a.TZ, Locale: a.Locale, Network: a.Network, Switches: a.viewSwitches(),
		Metadata:   copyMetadata(a.Metadata),
		LastSyncAt: a.LastSyncAt, LastErrorAt: a.LastErrorAt,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version,
	}
	if a.Owner != nil && a.Owner.NodeID != "" {
		v.OwnerNode = a.Owner.NodeID
	}
	return v
}

// viewSwitches copies switches and forces protocolLogs.rawDebug off when RawDebugUntil has expired.
func (a Account) viewSwitches() map[string]any {
	if a.Switches == nil {
		return nil
	}
	if a.rawDebugActive() {
		return a.Switches
	}
	v, ok := a.Switches["protocolLogs.rawDebug"].(bool)
	if !ok || !v {
		return a.Switches
	}
	out := make(map[string]any, len(a.Switches))
	for k, val := range a.Switches {
		out[k] = val
	}
	out["protocolLogs.rawDebug"] = false
	return out
}

func (a Account) rawDebugActive() bool {
	return a.RawDebugUntil != nil && time.Now().UTC().Before(*a.RawDebugUntil)
}

// OAuthLink is the part of accounts.oauth the accounts package needs: whether the account signs in with OAuth.
type OAuthLink struct {
	AppID string `bson:"appId"`
}

// IdentityPin is accounts.identity (P6.4) — expected mailbox email + IMAP user / OAuth subject.
type IdentityPin struct {
	Email        string    `bson:"email" json:"email" doc:"Mailbox email address seen at pinning." example:"reception@example.com"`
	IMAPUser     string    `bson:"imapUser" json:"imapUser" doc:"IMAP login user seen at pinning." example:"reception@example.com"`
	OAuthSubject string    `bson:"oauthSubject,omitempty" json:"oauthSubject,omitempty" doc:"OAuth subject (sub claim) of the signed-in mailbox, for OAuth accounts." example:"110248495921238986420"`
	PinnedAt     time.Time `bson:"pinnedAt" json:"pinnedAt" doc:"When the identity was pinned." example:"2026-10-20T10:02:15Z"`
}

// UsesOAuth reports whether the account authenticates with OAuth tokens instead of passwords.
func (a *Account) UsesOAuth() bool {
	if a == nil {
		return false
	}
	if a.Delegated != nil && a.Delegated.Account != "" {
		return true
	}
	return a.OAuth != nil && a.OAuth.AppID != ""
}

// UsesAuthServer reports whether credentials are fetched from the external credential server.
func (a *Account) UsesAuthServer() bool { return a != nil && a.UseAuthServer }
