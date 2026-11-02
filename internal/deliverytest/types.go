// Package deliverytest runs probe sends and reports folder placement + auth results (P5.3).
package deliverytest

import (
	"encoding/json"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/msgauth"
)

// Collection is deliveryTests (S1 §6).
const Collection = "deliveryTests"

// JobKind is the jobs handler name.
const JobKind = "deliveryTest"

const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateTimedOut  = "timedOut"
)

const (
	FolderInbox = "inbox"
	FolderSpam  = "spam"
	FolderOther = "other"
)

// Test is the stored deliveryTests document.
type Test struct {
	ID             string           `bson:"_id" json:"id"`
	OrganizationID string           `bson:"organizationId" json:"organizationId"`
	AccountID      string           `bson:"accountId" json:"accountId"`
	To             string           `bson:"to" json:"to"`
	State          string           `bson:"state" json:"state"`
	OutboundID     string           `bson:"outboundId,omitempty" json:"outboundId,omitempty"`
	MessageID      string           `bson:"messageId,omitempty" json:"messageId,omitempty"`
	Folder         string           `bson:"folder,omitempty" json:"folder,omitempty"`
	MailboxPath    string           `bson:"mailboxPath,omitempty" json:"mailboxPath,omitempty"`
	LatencyMs      int64            `bson:"latencyMs,omitempty" json:"latencyMs,omitempty"`
	AuthResults    *msgauth.Results `bson:"authResults,omitempty" json:"authResults,omitempty"`
	Error          *errs.Problem    `bson:"-" json:"error,omitempty"`
	ErrorJSON      string           `bson:"error,omitempty" json:"-"`
	JobID          string           `bson:"jobId,omitempty" json:"jobId,omitempty"`
	Timeout        time.Duration    `bson:"timeout,omitempty" json:"-"`
	CreatedAt      time.Time        `bson:"createdAt" json:"createdAt"`
	FinishedAt     *time.Time       `bson:"finishedAt,omitempty" json:"finishedAt,omitempty"`
	CreatedBy      string           `bson:"createdBy,omitempty" json:"-"`
}

// CreateInput is POST /v1/delivery-tests.
type CreateInput struct {
	AccountID string `json:"accountId" doc:"Account (acc_…) that sends the probe and in whose index it is looked for." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	To        string `json:"to" doc:"Probe recipient; must be delivered back into the same account (its own address or an alias). In production only the configured test mailbox (EB_TEST_MAIL_USER) is allowed." example:"reception@example.com"`
	Timeout   string `json:"timeout,omitempty" doc:"How long to wait for the probe to arrive (Go duration, at most 10m). Default 2m." example:"3m"`
}

// Accepted is the 202 body.
type Accepted struct {
	ID    string `json:"id" doc:"Delivery test ID (dtt_…); poll GET /v1/delivery-tests/{testId}." example:"dtt_01m5t8wrq8xkam085mr0twjs74"`
	JobID string `json:"jobId" doc:"Job that runs the test." example:"job_01m5cnaee0w96fc46z350nc6q0"`
	State string `json:"state" doc:"Always queued." example:"queued"`
}

// View is the API representation.
type View struct {
	ID             string           `json:"id" doc:"Delivery test ID (dtt_…)." example:"dtt_01m5t8wrq8xkam085mr0twjs74"`
	OrganizationID string           `json:"organizationId" doc:"Organisation of the test." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      string           `json:"accountId" doc:"Sending and receiving account." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	To             string           `json:"to" doc:"Probe recipient." example:"reception@example.com"`
	State          string           `json:"state" doc:"queued, running, succeeded, failed or timedOut." example:"succeeded"`
	OutboundID     string           `json:"outboundId,omitempty" doc:"Outbound message (out_…) of the probe." example:"out_01m5drrjzg6m505g6fban69akn"`
	MessageID      string           `json:"messageId,omitempty" doc:"Message-ID header of the probe." example:"<20261027101500.dtt_01m5t8wrq8xkam085mr0twjs74@example.com>"`
	Folder         string           `json:"folder,omitempty" doc:"Where the probe landed: inbox, spam or other." example:"inbox"`
	MailboxPath    string           `json:"mailboxPath,omitempty" doc:"Mailbox path the probe was found in." example:"INBOX"`
	LatencyMs      int64            `json:"latencyMs,omitempty" doc:"Time from submit until the probe was indexed, in milliseconds." example:"8421"`
	AuthResults    *msgauth.Results `json:"authResults,omitempty" doc:"SPF / DKIM / DMARC / ARC results read from the received probe."`
	Error          *errs.Problem    `json:"error,omitempty" doc:"Why the test failed or timed out (RFC 9457 problem)."`
	JobID          string           `json:"jobId,omitempty" doc:"Job that ran the test." example:"job_01m5cnaee0w96fc46z350nc6q0"`
	CreatedAt      time.Time        `json:"createdAt" doc:"When the test was started." example:"2026-10-27T10:15:00Z"`
	FinishedAt     *time.Time       `json:"finishedAt,omitempty" doc:"When it reached a final state." example:"2026-10-27T10:15:08Z"`
}

// ViewOf maps a stored test.
func ViewOf(t *Test) View {
	if t.Error == nil && t.ErrorJSON != "" {
		var p errs.Problem
		_ = json.Unmarshal([]byte(t.ErrorJSON), &p)
		t.Error = &p
	}
	return View{
		ID: t.ID, OrganizationID: t.OrganizationID, AccountID: t.AccountID, To: t.To, State: t.State,
		OutboundID: t.OutboundID, MessageID: t.MessageID, Folder: t.Folder, MailboxPath: t.MailboxPath,
		LatencyMs: t.LatencyMs, AuthResults: t.AuthResults, Error: t.Error, JobID: t.JobID,
		CreatedAt: t.CreatedAt, FinishedAt: t.FinishedAt,
	}
}

// jobPayload is stored on the jobs document.
type jobPayload struct {
	TestID string `json:"testId"`
}
