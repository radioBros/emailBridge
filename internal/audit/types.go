// Package audit is the append-only audit log (S1 auditLog, S12 §4, S2 GET /v1/audit-log).
package audit

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Collection is the MongoDB collection name (S1).
const Collection = "auditLog"

// ActorType is who performed an action.
type ActorType string

// Actor types (S1).
const (
	ActorUser   ActorType = "user"
	ActorAPIKey ActorType = "apiKey"
	ActorSystem ActorType = "system"
)

// Actor is the principal recorded on an audit entry.
type Actor struct {
	Type      ActorType `bson:"type" json:"type" enum:"user,apiKey,system" doc:"Kind of principal." example:"user"`
	ID        string    `bson:"id" json:"id" doc:"User ID (usr_…), API key ID (key_…) or system component name." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	IP        string    `bson:"ip,omitempty" json:"ip,omitempty" doc:"Client IP of the request." example:"203.0.113.24"`
	UserAgent string    `bson:"userAgent,omitempty" json:"userAgent,omitempty" doc:"Client user agent." example:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"`
}

// Target is the resource an action affected.
type Target struct {
	Type string `bson:"type" json:"type" doc:"Resource type (account, membership, apiKey, organization, settings …)." example:"account"`
	ID   string `bson:"id" json:"id" doc:"Resource ID." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

// FieldChange is one field's before/after (secrets as "[redacted]").
type FieldChange struct {
	Before any `bson:"before,omitempty" json:"before,omitempty" doc:"Value before the change (absent on create; secrets as [redacted])." example:"\"idle\""`
	After  any `bson:"after,omitempty" json:"after,omitempty" doc:"Value after the change (absent on delete)." example:"\"disabled\""`
}

// Changes is a field-level diff keyed by JSON-tag / map key names.
type Changes map[string]FieldChange

// Secret marks a value that must never be stored in the audit log.
type Secret string

// Record is one append-only auditLog document (S1).
type Record struct {
	ID             string     `bson:"_id" json:"id" doc:"Audit record ID (aud_…)." example:"aud_01m5d44p70hwn3w76n3fzr2gaa"`
	OrganizationID *string    `bson:"organizationId" json:"organizationId" doc:"Organisation of the action; null for instance-level actions." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	At             time.Time  `bson:"at" json:"at" doc:"When it happened (RFC 3339, UTC)." example:"2026-10-24T14:32:10Z"`
	Actor          Actor      `bson:"actor" json:"actor" doc:"Who did it."`
	Action         string     `bson:"action" json:"action" doc:"What was done (resource.verb)." example:"account.updated"`
	Target         Target     `bson:"target" json:"target" doc:"What it was done to."`
	Changes        Changes    `bson:"changes,omitempty" json:"changes,omitempty" doc:"Changed fields with before / after values; secrets are never stored." example:"{\"state\":{\"before\":\"idle\",\"after\":\"disabled\"}}"`
	RequestID      string     `bson:"requestId,omitempty" json:"requestId,omitempty" doc:"Request that caused it (X-Request-Id)." example:"req_01m5d44p70hwn3w76n3fzr2gaa"`
	TraceID        string     `bson:"traceId,omitempty" json:"traceId,omitempty" doc:"W3C trace ID of that request." example:"4bf92f3577b34da6a3ce929d0e0e4736"`
	ExpireAt       *time.Time `bson:"expireAt,omitempty" json:"-"`
}

// indexes returns the S1 indexes plus the TTL on expireAt.
func indexes() []bson.D {
	return []bson.D{
		{{Key: "organizationId", Value: 1}, {Key: "at", Value: -1}},
		{{Key: "target.id", Value: 1}, {Key: "at", Value: -1}},
		{{Key: "actor.id", Value: 1}, {Key: "at", Value: -1}},
	}
}
