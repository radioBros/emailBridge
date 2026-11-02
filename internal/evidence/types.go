// Package evidence archives PEC receipts and envelopes under S3 Object Lock with optional RFC 3161 stamps (S17 §9, P9.2).
package evidence

import (
	"time"
)

// DefaultRetention is ~10 years (3650 days) when pec.evidenceRetention is unset.
const DefaultRetention = 3650 * 24 * time.Hour

// TSARetryMaxAge is the maximum window for evidence.tsa-retry jobs.
const TSARetryMaxAge = 24 * time.Hour

// JobKindTSARetry is the jobs runner kind for deferred TSA stamping.
const JobKindTSARetry = "evidence.tsa-retry"

// Item is one object to archive.
type Item struct {
	ID           string
	Organization string
	Account      string
	Raw          []byte
	ReceivedAt   time.Time
	Type         string
	TransportID  string
	Signature    map[string]any // opaque; stored in the manifest as-is
	TSAURL       string         // empty = no stamp attempt
	Retention    time.Duration  // 0 = DefaultRetention / Options.Retention
}

// Ref points at archived evidence.
type Ref struct {
	Key         string     `json:"key"`
	ManifestKey string     `json:"manifestKey"`
	TSRKey      string     `json:"tsrKey,omitempty"`
	SHA256      string     `json:"sha256"`
	LockedUntil *time.Time `json:"lockedUntil,omitempty"`
	TSA         bool       `json:"tsa,omitempty"`
}

// Manifest is stored next to the .eml.
type Manifest struct {
	ID          string         `json:"id"`
	SHA256      string         `json:"sha256"`
	Size        int64          `json:"size"`
	ReceivedAt  time.Time      `json:"receivedAt"`
	Account     string         `json:"account"`
	Type        string         `json:"type"`
	TransportID string         `json:"transportId,omitempty"`
	Signature   map[string]any `json:"signature,omitempty"`
	TSA         *ManifestTSA   `json:"tsa,omitempty"`
}

// ManifestTSA records a successful stamp.
type ManifestTSA struct {
	At     time.Time `json:"at"`
	SHA256 string    `json:"sha256"` // hex of the .tsr bytes
}

// ArchiveCheck is the Verify outcome (S17 §9).
type ArchiveCheck struct {
	Intact      bool       `json:"intact" doc:"The archived object, its manifest and the receipt agree (all checks passed)" example:"true"`
	HashMatches bool       `json:"hashMatches" doc:"SHA-256 of the archived object equals the manifest and the receipt" example:"true"`
	Locked      bool       `json:"locked" doc:"The object is under Object Lock retention" example:"true"`
	LockedUntil *time.Time `json:"lockedUntil,omitempty" doc:"Object Lock retention end" example:"2036-10-27T08:30:09Z"`
	TSA         *TSAInfo   `json:"tsa" doc:"RFC 3161 timestamp check; null when no token is archived"` // null when no token
}

// TSAInfo is the verified (or failed) token summary.
type TSAInfo struct {
	Valid bool       `json:"valid" doc:"The timestamp token verifies against the archived object" example:"true"`
	At    *time.Time `json:"at,omitempty" doc:"Time asserted by the timestamp authority" example:"2026-10-27T08:30:10Z"`
}

// TSARetryPayload is the jobs payload for JobKindTSARetry.
type TSARetryPayload struct {
	OrganizationID string    `json:"organizationId"`
	AccountID      string    `json:"accountId"`
	ID             string    `json:"id"`
	Key            string    `json:"key"` // .eml key
	SHA256         string    `json:"sha256"`
	TSAURL         string    `json:"tsaUrl"`
	FirstAttemptAt time.Time `json:"firstAttemptAt"`
}
