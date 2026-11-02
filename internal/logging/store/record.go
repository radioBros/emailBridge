package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// LogRecord is a record as stored, as exchanged between nodes and as rendered by the API (OpenAPI
// LogRecord, S12 §3): time in nanoseconds UTC, correlation IDs flattened and null when absent.
type LogRecord struct {
	ID             string         `json:"id" pattern:"^log_[0-9a-z]{26}$" doc:"Log record ID (log_…)." example:"log_01m5dgv610fxn1vjp2m10r759f"`
	Time           int64          `json:"time" format:"int64" doc:"Nanoseconds UTC since the Unix epoch." example:"1792845130123456789"`
	Level          string         `json:"level" enum:"trace,debug,info,warn,error,fatal" doc:"Severity." example:"warn"`
	Category       string         `json:"category" doc:"Log category (system, api, auth, audit, event, sync, protocol, send, delivery, rules, job, cluster, integration, security, crash)." example:"sync"`
	Component      string         `json:"component" doc:"Component that wrote the record." example:"sync.imap"`
	Node           string         `json:"node,omitempty" doc:"Node ID." example:"eb-node-1"`
	Role           string         `json:"role,omitempty" doc:"Role the component runs in (api, sync, submit, webhook, scheduler)." example:"sync"`
	Msg            string         `json:"msg" doc:"Message." example:"IDLE connection dropped; reconnecting"`
	OrganizationID *string        `json:"organizationId" required:"false" nullable:"true" doc:"Organisation; null for instance-level records." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      *string        `json:"accountId" required:"false" nullable:"true" doc:"Account concerned." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	RequestID      *string        `json:"requestId" required:"false" nullable:"true" doc:"HTTP request ID." example:"req_01m5dexqe0yceck9xvvwfm24j9"`
	TraceID        *string        `json:"traceId" required:"false" nullable:"true" doc:"W3C trace ID." example:"4bf92f3577b34da6a3ce929d0e0e4736"`
	SpanID         *string        `json:"spanId" required:"false" nullable:"true" doc:"W3C span ID." example:"00f067aa0ba902b7"`
	EventID        *string        `json:"eventId" required:"false" nullable:"true" doc:"Event concerned." example:"evt_01m5ccvej0dqefkn0xhkcq2q6z"`
	DeliveryID     *string        `json:"deliveryId" required:"false" nullable:"true" doc:"Webhook delivery concerned." example:"dlv_01m5dsa5x0t7h8h17qhvq092rb"`
	OutboundID     *string        `json:"outboundId" required:"false" nullable:"true" doc:"Outbound message concerned." example:"out_01m5dq6dy05h78941bhj7x7pzr"`
	Actor          *string        `json:"actor" required:"false" nullable:"true" doc:"User or API key that caused it." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	JobID          *string        `json:"jobId" required:"false" nullable:"true" doc:"Background job concerned." example:"job_01m5cnaee0w96fc46z350nc6q0"`
	Kind           *string        `json:"kind" required:"false" nullable:"true" doc:"Error kind for error records (validation, auth, temporary, permanent …)." example:"temporary"`
	Code           *string        `json:"code" required:"false" nullable:"true" doc:"Problem code for error records." example:"mail_connect_failed"`
	Attrs          map[string]any `json:"attrs,omitempty" doc:"Further structured attributes (redacted)." example:"{\"mailbox\":\"INBOX\",\"attempt\":3}"`
	Stack          *string        `json:"stack" required:"false" nullable:"true" doc:"Stack trace (crash records only)." example:"goroutine 81 [running]: main.(*worker).run(...) internal/sync/worker.go:212"`
}

// Position is a keyset position: records are ordered by (time, id) descending.
type Position struct {
	Time int64  `json:"time"`
	ID   string `json:"id"`
}

// Pos returns the record's keyset position.
func (r *LogRecord) Pos() Position { return Position{Time: r.Time, ID: r.ID} }

// Before reports whether p sorts after q in newest-first order (p is older than q).
func (p Position) Before(q Position) bool {
	if p.Time != q.Time {
		return p.Time < q.Time
	}
	return p.ID < q.ID
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// FromRecord converts a log-system record into its stored/API form.
func FromRecord(r *logging.Record) LogRecord {
	level := r.LevelName
	if level == "" {
		level = logging.LevelName(r.Level)
	}
	cat := r.Category
	if !cat.Valid() {
		cat = logging.CatSystem
	}
	return LogRecord{
		ID: r.ID, Time: r.Time.UTC().UnixNano(), Level: level, Category: string(cat), Component: r.Component,
		Node: r.Node, Role: r.Role, Msg: r.Msg,
		OrganizationID: ptr(r.IDs.OrganizationID), AccountID: ptr(r.IDs.AccountID), RequestID: ptr(r.IDs.RequestID),
		TraceID: ptr(r.IDs.TraceID), SpanID: ptr(r.IDs.SpanID), EventID: ptr(r.IDs.EventID),
		DeliveryID: ptr(r.IDs.DeliveryID), OutboundID: ptr(r.IDs.OutboundID), Actor: ptr(r.IDs.Actor),
		JobID: ptr(r.IDs.JobID), Kind: ptr(r.Kind), Code: ptr(r.Code), Attrs: r.Attrs, Stack: ptr(r.Stack),
	}
}

// attrsJSON renders attributes for the attrs column (NULL when there are none). Values the redactor
// produced are JSON-able; anything that still fails to marshal is replaced by a marker, never dropped
// silently.
func attrsJSON(attrs map[string]any) any {
	if len(attrs) == 0 {
		return nil
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return `{"_attrsError":"attributes not serialisable"}`
	}
	return string(b)
}

func nullable(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

// insertArgs returns the insertSQL arguments for r.
func insertArgs(r *LogRecord) []any {
	lvl, err := logging.ParseLevel(r.Level)
	if err != nil {
		lvl = logging.LevelInfo
	}
	return []any{r.ID, r.Time, r.Level, int64(lvl), r.Category, r.Component, nullable(ptr(r.Node)), nullable(ptr(r.Role)), r.Msg,
		nullable(r.OrganizationID), nullable(r.AccountID), nullable(r.RequestID), nullable(r.TraceID), nullable(r.SpanID),
		nullable(r.EventID), nullable(r.DeliveryID), nullable(r.OutboundID), nullable(r.Actor), nullable(r.JobID),
		nullable(r.Kind), nullable(r.Code), attrsJSON(r.Attrs), nullable(r.Stack)}
}

type rowScanner interface{ Scan(dest ...any) error }

// scanRecord reads one selectCols row.
func scanRecord(sc rowScanner) (LogRecord, error) {
	var (
		r                                                    LogRecord
		node, role, org, acc, req, trace, span, evt, dlv     sql.NullString
		out, actor, job, kind, code, attrs, stack, component sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.Time, &r.Level, &r.Category, &component, &node, &role, &r.Msg, &org, &acc, &req, &trace,
		&span, &evt, &dlv, &out, &actor, &job, &kind, &code, &attrs, &stack); err != nil {
		return r, fmt.Errorf("logstore: scan: %w", err)
	}
	ns := func(n sql.NullString) *string {
		if !n.Valid {
			return nil
		}
		return ptr(n.String)
	}
	r.Component, r.Node, r.Role = component.String, node.String, role.String
	r.OrganizationID, r.AccountID, r.RequestID, r.TraceID, r.SpanID = ns(org), ns(acc), ns(req), ns(trace), ns(span)
	r.EventID, r.DeliveryID, r.OutboundID, r.Actor, r.JobID = ns(evt), ns(dlv), ns(out), ns(actor), ns(job)
	r.Kind, r.Code, r.Stack = ns(kind), ns(code), ns(stack)
	if attrs.Valid && attrs.String != "" {
		dec := json.NewDecoder(strings.NewReader(attrs.String))
		dec.UseNumber() // keep int64 attributes exact
		var m map[string]any
		if err := dec.Decode(&m); err == nil {
			r.Attrs = m
		}
	}
	return r, nil
}

// Filter selects records. It is the node-local form of a validated API query and travels in the
// eb.logs.query envelope. Categories and Level must be valid; callers validate (the query package does).
type Filter struct {
	From       time.Time          `json:"from"` // inclusive
	To         time.Time          `json:"to"`   // exclusive
	Categories []logging.Category `json:"categories,omitempty"`
	Level      string             `json:"level,omitempty"` // minimum level name; "" = all
	OrgID      string             `json:"organizationId,omitempty"`
	AccountID  string             `json:"accountId,omitempty"`
	RequestID  string             `json:"requestId,omitempty"`
	EventID    string             `json:"eventId,omitempty"`
	DeliveryID string             `json:"deliveryId,omitempty"`
	OutboundID string             `json:"outboundId,omitempty"`
	TraceID    string             `json:"traceId,omitempty"`
	Q          string             `json:"q,omitempty"`
	Before     *Position          `json:"before,omitempty"` // keyset: only records strictly older
	Limit      int                `json:"limit,omitempty"`
	levelNum   *slog.Level        // cached by minLevel
	catSet     map[logging.Category]bool
}

// minLevel returns the minimum level, or nil when every level matches.
func (f *Filter) minLevel() (*slog.Level, error) {
	if f.Level == "" {
		return nil, nil
	}
	if f.levelNum != nil {
		return f.levelNum, nil
	}
	l, err := logging.ParseLevel(f.Level)
	if err != nil {
		return nil, err
	}
	f.levelNum = &l
	return f.levelNum, nil
}

// categories returns the selected categories (all when none are given), skipping unknown names so a
// category can never be used to build a path outside the store.
func (f *Filter) categories() []logging.Category {
	if len(f.Categories) == 0 {
		return logging.Categories
	}
	out := make([]logging.Category, 0, len(f.Categories))
	seen := map[logging.Category]bool{}
	for _, c := range f.Categories {
		if c.Valid() && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// Matches reports whether r passes the filter, for live tail (no time range). Q is a case-insensitive
// substring match on msg and attributes — an approximation of the stored FTS5 phrase/prefix search.
func (f *Filter) Matches(r *LogRecord) bool {
	if len(f.Categories) > 0 {
		if f.catSet == nil {
			f.catSet = map[logging.Category]bool{}
			for _, c := range f.Categories {
				f.catSet[c] = true
			}
		}
		if !f.catSet[logging.Category(r.Category)] {
			return false
		}
	}
	if ml, err := f.minLevel(); err != nil {
		return false
	} else if ml != nil {
		l, err := logging.ParseLevel(r.Level)
		if err != nil || l < *ml {
			return false
		}
	}
	for _, c := range [...]struct {
		want string
		got  *string
	}{{f.OrgID, r.OrganizationID}, {f.AccountID, r.AccountID}, {f.RequestID, r.RequestID}, {f.EventID, r.EventID},
		{f.DeliveryID, r.DeliveryID}, {f.OutboundID, r.OutboundID}, {f.TraceID, r.TraceID}} {
		if c.want != "" && str(c.got) != c.want {
			return false
		}
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		needle := strings.ToLower(q)
		if strings.Contains(strings.ToLower(r.Msg), needle) {
			return true
		}
		if len(r.Attrs) == 0 {
			return false
		}
		b, err := json.Marshal(r.Attrs)
		return err == nil && bytes.Contains(bytes.ToLower(b), []byte(needle))
	}
	return true
}

// FTSQuery turns user input into a safe FTS5 expression: the whole input is one phrase whose last token
// is a prefix ("connection ref" matches "connection refused"). Double quotes are doubled, so no FTS5
// operator, column filter or syntax can be injected. ok is false when the input holds no searchable
// characters (then the search clause is dropped).
func FTSQuery(q string) (expr string, ok bool) {
	q = strings.TrimSpace(q)
	searchable := false
	for _, r := range q {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127 {
			searchable = true
			break
		}
	}
	if !searchable {
		return "", false
	}
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"*`, true
}
