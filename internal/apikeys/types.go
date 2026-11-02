// Package apikeys manages EmailBridge API keys on top of Limen's api-key plugin (S10 §3, S1 apiKeys).
package apikeys

import (
	"encoding/json"
	"time"
)

// Collection is the MongoDB collection name (S1).
const Collection = "apiKeys"

// limenAPIKeys is Limen's auth_api_keys collection (prefix auth_ + table api_keys).
const limenAPIKeys = "auth_api_keys" // #nosec G101 -- MongoDB collection name, not a credential

// Limen cache key prefix as configured in internal/auth (CacheKeyPrefix "limen").
const limenCachePrefix = "limen"

// profileID is the Limen key profile registered by auth (prefix ebk_).
const profileID = "default"

// Key is S1 apiKeys as returned by the API (_id → id).
type Key struct {
	ID             string        `bson:"_id" json:"id" doc:"API key ID (key_…)." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
	OrganizationID string        `bson:"organizationId" json:"organizationId" doc:"Organisation the key acts in (fixed at creation)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string        `bson:"name" json:"name" doc:"Display name." example:"CRM integration"`
	LimenKeyID     string        `bson:"limenKeyId" json:"-"`
	LimenKeyHash   string        `bson:"limenKeyHash" json:"-"`
	LimenPrincipal string        `bson:"limenPrincipalId" json:"-"`
	Prefix         string        `bson:"prefix" json:"prefix" doc:"Visible prefix of the secret (every key starts with ebk_)." example:"ebk_"`
	Last4          string        `bson:"last4" json:"last4" doc:"Last four characters of the secret, to recognise the key." example:"x3Qa"`
	Scopes         []string      `bson:"scopes" json:"scopes" doc:"Granted scopes: <resource>:read|write, <resource>:read|write:<id> for one account / endpoint / route / template, oauth:token:export, mcp:use, or admin." example:"[\"accounts:read\",\"messages:read\",\"outbound:write\"]"`
	AccountID      string        `bson:"accountId,omitempty" json:"accountId,omitempty" doc:"When set, the key is bound to this account: it sees and acts only on it." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Restrictions   *Restrictions `bson:"restrictions,omitempty" json:"restrictions,omitempty" doc:"Client restrictions checked on every request."`
	RateLimit      *RateLimit    `bson:"rateLimit,omitempty" json:"rateLimit,omitempty" doc:"Per-key rate limit hint stored with the key."`
	ExpiresAt      *time.Time    `bson:"expiresAt,omitempty" json:"expiresAt,omitempty" doc:"When the key stops working; absent = never." example:"2027-10-27T00:00:00Z"`
	LastUsedAt     *time.Time    `bson:"lastUsedAt,omitempty" json:"lastUsedAt,omitempty" doc:"Last successful request with the key." example:"2026-10-27T09:41:18Z"`
	RevokedAt      *time.Time    `bson:"revokedAt,omitempty" json:"revokedAt,omitempty" doc:"When the key was revoked; revoked keys stay listed and fail with 401 api_key_revoked." example:"2026-10-27T12:00:00Z"`
	CreatedBy      string        `bson:"createdBy" json:"createdBy,omitempty" doc:"User or API key that created it." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt      time.Time     `bson:"createdAt" json:"createdAt" doc:"When the key was created." example:"2026-10-21T14:05:00Z"`
	UpdatedAt      time.Time     `bson:"updatedAt" json:"updatedAt" doc:"Last change." example:"2026-10-24T08:12:30Z"`
	Version        int64         `bson:"version" json:"version" doc:"Resource version; the ETag is \"v\" followed by this number." example:"2"`
}

// Restrictions are IP CIDR and referrer pattern limits (S10 §3).
type Restrictions struct {
	IPs       []string `bson:"ips,omitempty" json:"ips,omitempty" doc:"Allowed client IPs or CIDR ranges (IPv4 or IPv6); other clients get 403 api_key_ip_denied. Empty = any." example:"[\"203.0.113.0/24\",\"2001:db8::/32\",\"198.51.100.7\"]"`
	Referrers []string `bson:"referrers,omitempty" json:"referrers,omitempty" doc:"Allowed Referer patterns (shell-style globs: * matches within a path segment); other requests get 403 api_key_referrer_denied. Empty = any." example:"[\"https://crm.example.com/*\"]"`
}

// RateLimit is an optional per-key token bucket hint (S1).
type RateLimit struct {
	RPS   float64 `bson:"rps,omitempty" json:"rps,omitempty" doc:"Requests per second (token refill rate)." example:"10"`
	Burst int     `bson:"burst,omitempty" json:"burst,omitempty" doc:"Bucket size: requests allowed in a burst." example:"20"`
}

// CreateInput is POST /v1/api-keys.
type CreateInput struct {
	Name         string
	Scopes       []string
	AccountID    string
	Restrictions *Restrictions
	RateLimit    *RateLimit
	ExpiresAt    *time.Time
}

// PatchInput is PATCH /v1/api-keys/{keyId} (JSON Merge Patch).
type PatchInput struct {
	Name         *string
	Scopes       *[]string
	AccountID    *nullableString
	Restrictions *Restrictions
	RateLimit    *RateLimit
	ExpiresAt    *nullableTime
}

// nullableString distinguishes omit vs JSON null in a merge patch.
type nullableString struct {
	Present bool
	Null    bool
	Value   string
}

func (n *nullableString) UnmarshalJSON(b []byte) error {
	n.Present = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// ClearAccountID is a PATCH that removes the account binding.
func ClearAccountID() *nullableString { return &nullableString{Present: true, Null: true} }

// SetAccountID is a PATCH that binds the key to accountID.
func SetAccountID(accountID string) *nullableString {
	return &nullableString{Present: true, Value: accountID}
}

// nullableTime distinguishes omit vs JSON null for expiresAt.
type nullableTime struct {
	Present bool
	Null    bool
	Value   time.Time
}

func (n *nullableTime) UnmarshalJSON(b []byte) error {
	n.Present = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return err
		}
	}
	n.Value = t.UTC()
	return nil
}

// ClearExpiresAt is a PATCH that removes the expiry.
func ClearExpiresAt() *nullableTime { return &nullableTime{Present: true, Null: true} }

// SetExpiresAt is a PATCH that sets the expiry.
func SetExpiresAt(t time.Time) *nullableTime {
	return &nullableTime{Present: true, Value: t.UTC()}
}

// ListFilter holds GET /v1/api-keys query parameters.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string // name, createdAt, lastUsedAt, id (±); default -id
	Q            string // case-insensitive substring on name or prefix
}

// KeyList is a cursor/page list response.
type KeyList struct {
	Data        []Key   `json:"data" doc:"The API keys of this page (secrets are never included)."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6ImtleV8wMW01azYyeW4xZXg2emd0cm03MnM1NzZrZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Total number of matching keys (only with page and includeTotal)." example:"7"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}
