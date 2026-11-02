// Package sandbox implements the send-pipeline sandbox (S6 §7): capture / rewrite / drop / allow,
// the capturedMessages store, the sandbox API and retention of capture objects.
package sandbox

import (
	"time"

	"github.com/radiobros/emailbridge/internal/submit"
)

// Collection is the MongoDB collection of captured sandbox messages (S1 §4).
const Collection = "capturedMessages"

// Recipient policies (S6 §7).
const (
	PolicyCapture = "capture"
	PolicyRewrite = "rewrite"
	PolicyDrop    = "drop"
	PolicyAllow   = "allow"
)

// Captured is one capturedMessages document (S1 §4).
type Captured struct {
	ID             string          `bson:"_id" json:"id"`
	OrganizationID string          `bson:"organizationId" json:"organizationId"`
	AccountID      string          `bson:"accountId" json:"accountId"`
	OutboundID     string          `bson:"outboundId" json:"outboundId"`
	Envelope       submit.Envelope `bson:"envelope" json:"envelope"`
	Subject        string          `bson:"subject,omitempty" json:"subject,omitempty"`
	RawRef         string          `bson:"rawRef,omitempty" json:"-"`
	CapturedAt     time.Time       `bson:"capturedAt" json:"capturedAt"`
	ExpireAt       time.Time       `bson:"expireAt" json:"expireAt"`
}

// View is the API representation (OpenAPI CapturedMessage).
type View struct {
	ID             string          `json:"id" doc:"Capture ID (cap_…)." example:"cap_01m5dgv610dh6t59mc4867vabc"`
	OrganizationID string          `json:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      string          `json:"accountId" doc:"Sending account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	OutboundID     string          `json:"outboundId" doc:"The outbound message that was captured (out_…)." example:"out_01m5dgv610f4k7r2t9w3y5z8ab"`
	Envelope       submit.Envelope `json:"envelope" doc:"SMTP envelope the message would have been sent with."`
	Subject        string          `json:"subject,omitempty" doc:"Subject of the message." example:"Invoice 2026-1043"`
	CapturedAt     time.Time       `json:"capturedAt" doc:"When the sandbox captured the message." example:"2026-10-27T10:15:02Z"`
	ExpireAt       time.Time       `json:"expireAt,omitempty" doc:"When the capture is deleted (14 days after capture by default)." example:"2026-11-10T10:15:02Z"`
}

// ViewOf redacts a stored capture into its API form.
func ViewOf(c *Captured) View {
	return View{
		ID: c.ID, OrganizationID: c.OrganizationID, AccountID: c.AccountID, OutboundID: c.OutboundID,
		Envelope: c.Envelope, Subject: c.Subject, CapturedAt: c.CapturedAt, ExpireAt: c.ExpireAt,
	}
}

// List is the body of GET /v1/sandbox/messages (OpenAPI CapturedMessageList).
type List struct {
	Data        []View  `json:"data" doc:"The page of captures."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6ImNhcF8wMW01ZGVxZTIweWdwcXZmdzljancxY2hqYSIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Number of matching captures, with page and includeTotal=true (at most 10 000)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"true when total stopped at 10 000." example:"false"`
}

// ListFilter is the query of GET /v1/sandbox/messages.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	AccountID    string
	From         time.Time // capturedAt >= From
	To           time.Time // capturedAt < To
	Text         string    // subject substring (case-insensitive)
}
