// Package health diagnoses mailbox account health and exposes fix hints (P6.5).
package health

import (
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
)

// Severity ranks how urgent an account issue is.
type Severity string

const (
	SeverityOK       Severity = "ok"
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Rank returns a comparable severity rank (higher = worse).
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// MaxSeverity returns the worse of a and b.
func MaxSeverity(a, b Severity) Severity {
	if a.Rank() >= b.Rank() {
		return a
	}
	return b
}

// Hint codes (stable API keys).
const (
	HintAppPassword      = "app_password_required"
	HintIMAPDisabled     = "imap_disabled"
	HintWrongHostPortTLS = "wrong_host_port_tls"
	HintCertificate      = "certificate_problem"
	HintOAuthRevoked     = "oauth_consent_revoked"
	HintRateLimited      = "rate_limited"
	HintMailboxFull      = "mailbox_full"
	HintAuthFailed       = "auth_failed"
	HintConnectFailed    = "connect_failed"
	HintCapacity         = "capacity"
	HintIgnoreCertErrors = "ignore_cert_errors"
)

// Hint is one fix suggestion keyed by a known cause.
type Hint struct {
	Code     string   `json:"code" doc:"Stable hint code (app_password_required, imap_disabled, wrong_host_port_tls, certificate_problem, oauth_consent_revoked, rate_limited, mailbox_full, auth_failed …)." example:"app_password_required"`
	Severity Severity `json:"severity" enum:"ok,info,warning,critical" doc:"How serious the finding is." example:"critical"`
	Title    string   `json:"title" doc:"Short, human-readable finding." example:"The provider requires an app password"`
	Detail   string   `json:"detail" doc:"What to do about it." example:"Create an app password in the Google account security settings and use it instead of the normal password."`
	DocsPath string   `json:"docsPath,omitempty" doc:"Path of the documentation page that explains the fix." example:"/docs/troubleshooting/app-passwords"`
}

// QuotaFact is mailbox quota when known.
type QuotaFact struct {
	Used  *int64 `json:"used,omitempty" doc:"Bytes used (IMAP QUOTA)." example:"14495514624"`
	Limit *int64 `json:"limit,omitempty" doc:"Quota in bytes." example:"16106127360"`
}

// ProtocolFacts summarises connection/capability facts for the UI.
type ProtocolFacts struct {
	TLSMode          string     `json:"tlsMode,omitempty" doc:"How the IMAP connection is secured (tls, starttls, none)." example:"tls"`
	AuthUser         string     `json:"authUser,omitempty" doc:"Login user name." example:"reception@example.com"`
	HasPassword      bool       `json:"hasPassword" doc:"Whether a password is stored (never returned)." example:"true"`
	OAuthLinked      bool       `json:"oauthLinked" doc:"Whether an OAuth grant is linked." example:"false"`
	Provider         string     `json:"provider,omitempty" doc:"Detected mail provider." example:"gmail"`
	Idle             bool       `json:"idle" doc:"Server supports IMAP IDLE." example:"true"`
	Condstore        bool       `json:"condstore" doc:"Server supports CONDSTORE." example:"true"`
	QResync          bool       `json:"qresync" doc:"Server supports QRESYNC." example:"false"`
	AuthMechanisms   []string   `json:"authMechanisms,omitempty" doc:"SASL mechanisms the server offers." example:"[\"PLAIN\",\"XOAUTH2\"]"`
	Quota            *QuotaFact `json:"quota,omitempty" doc:"Mailbox quota when the server reports it."`
	FolderCount      int        `json:"folderCount" doc:"Folders known for the account." example:"14"`
	LastSyncAt       *time.Time `json:"lastSyncAt,omitempty" doc:"Last successful sync." example:"2026-10-27T09:58:13Z"`
	LastErrorAt      *time.Time `json:"lastErrorAt,omitempty" doc:"Last connection or sync error." example:"2026-10-27T07:12:40Z"`
	ConnectLatencyMs *int       `json:"connectLatencyMs,omitempty" doc:"Connect + login time of the last live check, in milliseconds." example:"184"`
}

// RecentErrorView is one timeline entry (API).
type RecentErrorView struct {
	At       time.Time `json:"at" doc:"When the error happened." example:"2026-10-27T07:12:40Z"`
	Kind     string    `json:"kind" doc:"Error kind (auth, temporary, config, permanent …)." example:"auth"`
	Code     string    `json:"code" doc:"Problem code." example:"mail_auth_failed"`
	Protocol string    `json:"protocol,omitempty" doc:"Protocol that failed (imap, smtp)." example:"imap"`
	Response string    `json:"response,omitempty" doc:"Server response, redacted." example:"NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)"`
	Hint     string    `json:"hint,omitempty" doc:"Matching hint code." example:"app_password_required"`
}

// LiveCheckError is a redacted live-check failure.
type LiveCheckError struct {
	Code    string `json:"code,omitempty" doc:"Problem code of the failure." example:"mail_auth_failed"`
	Kind    string `json:"kind,omitempty" doc:"Error kind." example:"auth"`
	Message string `json:"message,omitempty" doc:"Redacted message." example:"The IMAP server rejected the login."`
}

// LiveCheck is the outcome of POST …/health/check.
type LiveCheck struct {
	OK           bool            `json:"ok" doc:"Whether connect + login succeeded." example:"false"`
	LatencyMs    int             `json:"latencyMs,omitempty" doc:"Duration of the probe in milliseconds." example:"412"`
	Capabilities []string        `json:"capabilities,omitempty" doc:"IMAP capabilities reported by the server." example:"[\"IMAP4rev1\",\"IDLE\",\"CONDSTORE\",\"QUOTA\"]"`
	Error        *LiveCheckError `json:"error,omitempty" doc:"Why the probe failed."`
}

// StateReasonView mirrors accounts.StateReason for the health payload.
type StateReasonView struct {
	Kind    string    `json:"kind" doc:"Error kind behind the current state." example:"auth"`
	Code    string    `json:"code" doc:"Problem code." example:"mail_auth_failed"`
	Message string    `json:"message,omitempty" doc:"Redacted message." example:"Login failed: invalid credentials."`
	At      time.Time `json:"at" doc:"When the state was entered." example:"2026-10-27T07:12:40Z"`
}

// AccountHealth is GET/POST health response body.
type AccountHealth struct {
	AccountID    string            `json:"accountId" doc:"Account ID." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Name         string            `json:"name" doc:"Account display name." example:"Reception"`
	Email        string            `json:"email" doc:"Account email address." example:"reception@example.com"`
	State        string            `json:"state" doc:"Connection state of the account." example:"authError"`
	StateReason  *StateReasonView  `json:"stateReason,omitempty" doc:"Why the account is in its state (error states)."`
	Severity     Severity          `json:"severity" enum:"ok,info,warning,critical" doc:"Overall severity (worst hint)." example:"critical"`
	Summary      string            `json:"summary" doc:"One-line diagnosis." example:"Login rejected: the provider requires an app password."`
	Hints        []Hint            `json:"hints" doc:"Findings with concrete fixes, worst first."`
	Facts        ProtocolFacts     `json:"facts" doc:"Connection and capability facts."`
	RecentErrors []RecentErrorView `json:"recentErrors,omitempty" doc:"Recent errors, newest first."`
	LiveCheck    *LiveCheck        `json:"liveCheck,omitempty" doc:"Result of the live probe (POST …/health/check only)."`
	CheckedAt    time.Time         `json:"checkedAt" doc:"When this diagnosis was computed." example:"2026-10-27T10:05:00Z"`
}

// Counts is the org-wide severity breakdown.
type Counts struct {
	OK       int `json:"ok" doc:"Accounts without findings." example:"38"`
	Info     int `json:"info" doc:"Accounts with informational findings." example:"3"`
	Warning  int `json:"warning" doc:"Accounts with warnings." example:"2"`
	Critical int `json:"critical" doc:"Accounts with critical findings." example:"1"`
	Total    int `json:"total" doc:"All accounts counted." example:"44"`
}

// Row is one org-list entry.
type Row struct {
	AccountID   string     `json:"accountId" doc:"Account ID." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Name        string     `json:"name" doc:"Account display name." example:"Reception"`
	Email       string     `json:"email" doc:"Account email address." example:"reception@example.com"`
	State       string     `json:"state" doc:"Connection state." example:"authError"`
	Severity    Severity   `json:"severity" enum:"ok,info,warning,critical" doc:"Overall severity." example:"critical"`
	Summary     string     `json:"summary" doc:"One-line diagnosis." example:"Login rejected: the provider requires an app password."`
	HintCodes   []string   `json:"hintCodes" doc:"Codes of the findings." example:"[\"app_password_required\"]"`
	LastSyncAt  *time.Time `json:"lastSyncAt,omitempty" doc:"Last successful sync." example:"2026-10-26T18:40:02Z"`
	LastErrorAt *time.Time `json:"lastErrorAt,omitempty" doc:"Last error." example:"2026-10-27T07:12:40Z"`
}

// List is GET /v1/health/accounts.
type List struct {
	Counts Counts `json:"counts" doc:"Severity breakdown of the accounts matching q (the severity filter does not apply)."`
	Data   []Row  `json:"data" doc:"Accounts of this page."`
	Total  *int   `json:"total,omitempty" doc:"Rows matching the filter (with includeTotal)." example:"6"`
	Page   int    `json:"page" doc:"Page number returned." example:"1"`
	Limit  int    `json:"limit" doc:"Page size used." example:"50"`
}

// ListFilter controls the org health list.
type ListFilter struct {
	Page         int
	Limit        int
	IncludeTotal bool
	Sort         string
	Severity     Severity
	Q            string
}

func recentViews(errs []accounts.RecentError) []RecentErrorView {
	if len(errs) == 0 {
		return nil
	}
	out := make([]RecentErrorView, 0, len(errs))
	// Stored oldest→newest with $slice:-20; present newest first.
	for i := len(errs) - 1; i >= 0; i-- {
		e := errs[i]
		out = append(out, RecentErrorView{
			At: e.At, Kind: e.Kind, Code: e.Code, Protocol: e.Protocol,
			Response: e.Response, Hint: e.Hint,
		})
	}
	return out
}

func stateReasonView(r *accounts.StateReason) *StateReasonView {
	if r == nil {
		return nil
	}
	return &StateReasonView{Kind: r.Kind, Code: r.Code, Message: r.Message, At: r.At}
}
