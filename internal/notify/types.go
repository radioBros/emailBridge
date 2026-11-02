// Package notify is the system-notification engine (W75): problems with dedup keys,
// watchers, in-app / email / CloudEvents channels.
package notify

import "time"

// ScopeInstance marks instance-wide problems (visible only to instance owners).
const ScopeInstance = "instance"

// Categories (stable API / settings keys).
const (
	CatWebhooks     = "webhooks"
	CatQueues       = "queues"
	CatAccounts     = "accounts"
	CatSending      = "sending"
	CatOAuth        = "oauth"
	CatIntegrations = "integrations"
	CatPEC          = "pec"
	CatStorage      = "storage"
	CatPlatform     = "platform"
	CatSecurity     = "security"
	CatLimits       = "limits"
	CatInternal     = "internal"
)

// Severities.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// States.
const (
	StateOpen     = "open"
	StateResolved = "resolved"
)

// Collections.
const (
	Collection       = "notifications"
	ReadsCollection  = "notificationReads"
	EmailsCollection = "notificationEmails"
)

// Problem is the input to Raise (call-site or watcher).
type Problem struct {
	Scope        string // org id or ScopeInstance
	Key          string // dedup key
	Category     string
	Severity     string
	TitleKey     string
	DetailKey    string
	TitleParams  map[string]string
	DetailParams map[string]string
	Link         string
	Data         map[string]any
	Fingerprint  string
}

// Notification is the persisted problem row.
type Notification struct {
	ID             string            `bson:"_id" json:"id"`
	Scope          string            `bson:"scope" json:"scope"`
	OrganizationID string            `bson:"organizationId,omitempty" json:"organizationId,omitempty"`
	Key            string            `bson:"key" json:"key"`
	Category       string            `bson:"category" json:"category"`
	Severity       string            `bson:"severity" json:"severity"`
	State          string            `bson:"state" json:"state"`
	TitleKey       string            `bson:"titleKey" json:"titleKey"`
	DetailKey      string            `bson:"detailKey" json:"detailKey"`
	TitleParams    map[string]string `bson:"titleParams,omitempty" json:"titleParams,omitempty"`
	DetailParams   map[string]string `bson:"detailParams,omitempty" json:"detailParams,omitempty"`
	Link           string            `bson:"link,omitempty" json:"link,omitempty"`
	Data           map[string]any    `bson:"data,omitempty" json:"data,omitempty"`
	Occurrences    int               `bson:"occurrences" json:"occurrences"`
	FirstSeen      time.Time         `bson:"firstSeen" json:"firstSeen"`
	OpenedAt       time.Time         `bson:"openedAt,omitempty" json:"openedAt"` // first raise, or last re-open
	LastSeen       time.Time         `bson:"lastSeen" json:"lastSeen"`
	ResolvedAt     *time.Time        `bson:"resolvedAt,omitempty" json:"resolvedAt,omitempty"`
	Fingerprint    string            `bson:"fingerprint,omitempty" json:"fingerprint,omitempty"`
	Unread         bool              `bson:"-" json:"unread,omitempty"`
}

// ReadMark is per-user read state.
type ReadMark struct {
	ID             string    `bson:"_id"`
	UserID         string    `bson:"userId"`
	NotificationID string    `bson:"notificationId"`
	OrganizationID string    `bson:"organizationId,omitempty"`
	ReadAt         time.Time `bson:"readAt"`
}

// ListFilter is GET /v1/notifications.
type ListFilter struct {
	State        string
	Category     string
	Severity     string
	Unread       *bool
	From         *time.Time
	To           *time.Time
	Page         int
	Limit        int
	IncludeTotal bool
	Sort         string
}

// Page is a list response.
type Page struct {
	Data  []Notification `json:"data"`
	Page  int            `json:"page,omitempty"`
	Limit int            `json:"limit,omitempty"`
	Total *int64         `json:"total,omitempty"`
}

// Summary is GET /v1/notifications/summary.
type Summary struct {
	Unread     int            `json:"unread"`
	Open       int            `json:"open"`
	BySeverity map[string]int `json:"bySeverity"`
}

// EmailRecord remembers that one user was emailed about one notification (per-user, per-key throttle and
// the digest schedule). Stored in EmailsCollection, expires after 90 days.
type EmailRecord struct {
	ID             string    `bson:"_id"` // userId:notificationId
	UserID         string    `bson:"userId"`
	NotificationID string    `bson:"notificationId"`
	OrganizationID string    `bson:"organizationId,omitempty"` // empty for instance-scope problems
	Severity       string    `bson:"severity"`                 // severity at the time of the email
	Digest         bool      `bson:"digest"`                   // sent as part of a digest
	EmailedAt      time.Time `bson:"emailedAt"`
}

var knownCategories = map[string]bool{
	CatWebhooks: true, CatQueues: true, CatAccounts: true, CatSending: true,
	CatOAuth: true, CatIntegrations: true, CatPEC: true, CatStorage: true,
	CatPlatform: true, CatSecurity: true, CatLimits: true, CatInternal: true,
}

var knownSeverities = map[string]bool{
	SeverityInfo: true, SeverityWarning: true, SeverityCritical: true,
}

var severityRank = map[string]int{
	SeverityInfo: 1, SeverityWarning: 2, SeverityCritical: 3,
}
