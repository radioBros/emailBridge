package rules

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	RuleStatsCollection     = "ruleStats"
	RuleDecisionsCollection = "ruleDecisions"
	maxDecisionsPerRule     = 50
)

// RuleStats is the API/Mongo shape for counters.
type RuleStats struct {
	Evaluated     int64      `bson:"evaluated" json:"evaluated" doc:"Events evaluated by the rule" example:"18234"`
	Matched       int64      `bson:"matched" json:"matched" doc:"Events that matched" example:"1290"`
	Skipped       int64      `bson:"skipped" json:"skipped" doc:"Events that did not match" example:"16941"`
	Errors        int64      `bson:"errors" json:"errors" doc:"Evaluation errors" example:"3"`
	LastMatchedAt *time.Time `bson:"lastMatchedAt,omitempty" json:"lastMatchedAt,omitempty" doc:"Last match" example:"2026-10-27T09:14:22Z"`
	LastErrorAt   *time.Time `bson:"lastErrorAt,omitempty" json:"lastErrorAt,omitempty" doc:"Last error" example:"2026-10-25T17:02:11Z"`
	LastError     string     `bson:"lastError,omitempty" json:"lastError,omitempty" doc:"Last error text" example:"no such key: attachments"`
}

// RuleDecision is one recent evaluation outcome.
type RuleDecision struct {
	EventID string    `bson:"eventId" json:"eventId" doc:"Evaluated event (evt_…)" example:"evt_01m5czx69032gsbkp82n4ajd1w"`
	Matched bool      `bson:"matched" json:"matched" doc:"The filter matched" example:"true"`
	Reason  string    `bson:"reason,omitempty" json:"reason,omitempty" doc:"Why it did not match, when known (the first false term)" example:"fromDomain() in [\"example.org\"]"`
	Error   string    `bson:"error,omitempty" json:"error,omitempty" doc:"Evaluation error" example:"no such key: attachments"`
	At      time.Time `bson:"at" json:"at" doc:"When it was evaluated" example:"2026-10-27T09:14:22Z"`
}

// RuleDecisionList is GET …/decisions.
type RuleDecisionList struct {
	Data []RuleDecision `json:"data" doc:"Recent decisions, newest first (at most 50 are kept)."`
}

// StatsRecorder batches counter upserts and decision inserts. Observe only touches memory; Run flushes the
// batch to Mongo every second (and early when a batch grows), so the router path never waits on Mongo and
// every node's counters reach the shared collections (W73 §5).
type StatsRecorder struct {
	DB  *store.DB
	Log *slog.Logger

	mu      sync.Mutex
	pending map[string]*pendingStat
	prom    *prometheus.CounterVec
	kick    chan struct{}
	flushMu sync.Mutex // one flush at a time (ticker, early kick, read-path flush)
}

type pendingStat struct {
	org, kind, id string
	evaluated     int64
	matched       int64
	skipped       int64
	errors        int64
	lastMatched   *time.Time
	lastErrorAt   *time.Time
	lastError     string
	decisions     []RuleDecision
}

const (
	statsFlushEvery     = time.Second
	statsFlushThreshold = 64 // pending decisions that trigger an early flush
)

// NewStatsRecorder builds a recorder (Prometheus optional).
func NewStatsRecorder(db *store.DB) *StatsRecorder {
	return &StatsRecorder{
		DB:      db,
		pending: map[string]*pendingStat{},
		kick:    make(chan struct{}, 1),
		prom: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_rule_evaluations_total",
			Help: "CEL rule evaluations by result",
		}, []string{"org", "kind", "result"}),
	}
}

// Collector exposes the Prometheus counter for registration.
func (s *StatsRecorder) Collector() prometheus.Collector {
	if s == nil {
		return nil
	}
	return s.prom
}

func statsKey(kind, id string) string { return kind + ":" + id }

// Observe records one evaluation (kind = "route" | "endpoint"). Memory only; never blocks on I/O.
func (s *StatsRecorder) Observe(org, kind, id, eventID string, matched bool, errMsg string, reason string) {
	if s == nil {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	key := org + ":" + statsKey(kind, id)
	p := s.pending[key]
	if p == nil {
		p = &pendingStat{org: org, kind: kind, id: id}
		s.pending[key] = p
	}
	p.evaluated++
	result := "skipped"
	switch {
	case errMsg != "":
		p.errors++
		p.lastError = errMsg
		p.lastErrorAt = &now
		result = "error"
	case matched:
		p.matched++
		p.lastMatched = &now
		result = "matched"
	default:
		p.skipped++
	}
	early := false
	if eventID != "" {
		p.decisions = append(p.decisions, RuleDecision{
			EventID: eventID, Matched: matched, Reason: reason, Error: errMsg, At: now,
		})
		early = len(p.decisions) >= statsFlushThreshold
	}
	s.mu.Unlock()
	if s.prom != nil {
		s.prom.WithLabelValues(org, kind, result).Inc()
	}
	if early && s.kick != nil {
		select {
		case s.kick <- struct{}{}:
		default:
		}
	}
}

// Run flushes the pending batch every second until ctx ends, then flushes once more (shutdown).
func (s *StatsRecorder) Run(ctx context.Context) {
	if s == nil {
		return
	}
	t := time.NewTicker(statsFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			s.Flush(fctx)
			cancel()
			return
		case <-t.C:
			s.Flush(ctx)
		case <-s.kick:
			s.Flush(ctx)
		}
	}
}

// Flush writes pending stats to Mongo. The batch is swapped out under the lock and written outside it.
func (s *StatsRecorder) Flush(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	batch := s.pending
	s.pending = map[string]*pendingStat{}
	s.mu.Unlock()
	for _, p := range batch {
		s.write(ctx, p)
	}
}

func (s *StatsRecorder) write(ctx context.Context, p *pendingStat) {
	_id := p.org + ":" + statsKey(p.kind, p.id)
	inc := bson.M{"evaluated": p.evaluated, "matched": p.matched, "skipped": p.skipped, "errors": p.errors}
	set := bson.M{"organizationId": p.org, "kind": p.kind, "refId": p.id}
	if p.lastMatched != nil {
		set["lastMatchedAt"] = *p.lastMatched
	}
	if p.lastErrorAt != nil {
		set["lastErrorAt"] = *p.lastErrorAt
		set["lastError"] = p.lastError
	}
	if _, err := s.DB.Collection(RuleStatsCollection).UpdateOne(ctx,
		bson.M{"_id": _id, "organizationId": p.org},
		bson.M{"$inc": inc, "$set": set},
		options.UpdateOne().SetUpsert(true),
	); err != nil {
		s.logErr(ctx, "upsert", p, err)
	}
	if len(p.decisions) == 0 {
		return
	}
	if len(p.decisions) > maxDecisionsPerRule {
		p.decisions = p.decisions[len(p.decisions)-maxDecisionsPerRule:]
	}
	docs := make([]any, len(p.decisions))
	for i, d := range p.decisions {
		docs[i] = bson.M{
			"organizationId": p.org, "kind": p.kind, "refId": p.id,
			"eventId": d.EventID, "matched": d.Matched, "reason": d.Reason,
			"error": d.Error, "at": d.At,
		}
	}
	if _, err := s.DB.Collection(RuleDecisionsCollection).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false)); err != nil {
		s.logErr(ctx, "insertDecisions", p, err)
	}
	s.trimDecisions(ctx, p)
}

// trimDecisions keeps the newest maxDecisionsPerRule decisions of one rule.
func (s *StatsRecorder) trimDecisions(ctx context.Context, p *pendingStat) {
	q := bson.M{"organizationId": p.org, "kind": p.kind, "refId": p.id}
	cur, err := s.DB.Collection(RuleDecisionsCollection).Find(ctx, q,
		options.Find().SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
			SetSkip(maxDecisionsPerRule).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		s.logErr(ctx, "trimDecisions", p, err)
		return
	}
	var old []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &old); err != nil {
		s.logErr(ctx, "trimDecisions", p, err)
		return
	}
	if len(old) == 0 {
		return
	}
	ids := make([]bson.ObjectID, len(old))
	for i := range old {
		ids[i] = old[i].ID
	}
	if _, err := s.DB.Collection(RuleDecisionsCollection).DeleteMany(ctx,
		bson.M{"organizationId": p.org, "_id": bson.M{"$in": ids}}); err != nil {
		s.logErr(ctx, "trimDecisions", p, err)
	}
}

func (s *StatsRecorder) logErr(ctx context.Context, op string, p *pendingStat, err error) {
	if s.Log == nil {
		return
	}
	s.Log.ErrorContext(ctx, "rule stats write failed", slog.String("op", op), slog.String("organizationId", p.org),
		slog.String("ruleKind", p.kind), slog.String("ruleId", p.id), slog.Any("err", err))
}

// GetStats loads counters for a route or endpoint rule.
func (r *Repo) GetStats(ctx context.Context, kind, id string) (*RuleStats, error) {
	if err := requireStatsRead(ctx, kind, id); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	if r.Stats != nil {
		r.Stats.Flush(ctx)
	}
	var doc struct {
		RuleStats `bson:",inline"`
	}
	err := r.DB.Collection(RuleStatsCollection).FindOne(ctx, bson.M{
		"_id": c.OrganizationID + ":" + kind + ":" + id,
	}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return &RuleStats{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := doc.RuleStats
	return &st, nil
}

// ListDecisions returns recent decisions.
func (r *Repo) ListDecisions(ctx context.Context, kind, id string, limit int) (*RuleDecisionList, error) {
	if err := requireStatsRead(ctx, kind, id); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	if limit <= 0 || limit > maxDecisionsPerRule {
		limit = maxDecisionsPerRule
	}
	if r.Stats != nil {
		r.Stats.Flush(ctx)
	}
	cur, err := r.DB.Collection(RuleDecisionsCollection).Find(ctx,
		bson.M{"organizationId": c.OrganizationID, "kind": kind, "refId": id},
		options.Find().SetSort(bson.D{{Key: "at", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	var rows []RuleDecision
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return &RuleDecisionList{Data: rows}, nil
}

// EnsureStatsIndexes creates stats/decisions indexes (migration 45).
func EnsureStatsIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, RuleStatsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "kind", Value: 1}, {Key: "refId", Value: 1}}},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, RuleDecisionsCollection,
		mongo.IndexModel{Keys: bson.D{
			{Key: "organizationId", Value: 1}, {Key: "kind", Value: 1}, {Key: "refId", Value: 1}, {Key: "at", Value: -1},
		}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(30 * 24 * 3600),
		},
	)
}

// requireStatsRead checks the read scope of the rule owner: routes for a route, webhooks for an endpoint.
func requireStatsRead(ctx context.Context, kind, id string) error {
	scope := "routes"
	if kind == "endpoint" {
		scope = "webhooks"
	}
	return tenancy.RequireReadOn(ctx, scope, id)
}

// SampleEvent synthesises an event for {sample: type}: every field of the type's data schema (the Go type in
// the event catalogue, so zero values and nulls where a real event would have them), plus realistic values
// for the fields filters usually test (mailbox, labels, addresses, headers, ids, subject). Unknown types are
// a validation error.
func SampleEvent(typ string) (map[string]any, error) {
	ct, ok := events.Lookup(typ)
	if !ok || ct.Synthetic {
		return nil, errs.New(errs.CodeValidationFailed, "sample must be a known event type").WithSafe("sample", typ)
	}
	now := time.Now().UTC().Truncate(time.Second)
	data := map[string]any{}
	if ct.Data != nil {
		b, err := json.Marshal(reflect.New(ct.Data).Interface())
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &data); err != nil {
			return nil, err
		}
	}
	overlay := map[string]any{
		"accountId":       "acc_sample000000000000000000",
		"messageId":       "msg_sample000000000000000000",
		"outboundId":      "out_sample000000000000000000",
		"messageIdHeader": "<sample@example.com>",
		"subject":         "Sample " + typ,
		"mailbox":         map[string]any{"mailboxId": "mbx_sample000000000000000000", "path": "INBOX", "specialUse": `\Inbox`},
		"from":            map[string]any{"name": "Sample Sender", "address": "sample@example.com"},
		"to":              []any{map[string]any{"name": "You", "address": "you@example.com"}},
		"labels":          []any{`\Inbox`},
		"headers": map[string]any{
			"subject": []any{"Sample " + typ}, "message-id": []any{"<sample@example.com>"},
			"from": []any{"Sample Sender <sample@example.com>"},
		},
		"date":         now.Format(time.RFC3339),
		"internalDate": now.Format(time.RFC3339),
	}
	fields := jsonFields(ct.Data)
	for k, v := range overlay {
		if fields[k] {
			data[k] = v
		}
	}
	return map[string]any{
		"specversion": "1.0", "id": "evt_sample000000000000000000", "type": typ,
		"source": "/organizations/sample", "time": now.Format(time.RFC3339),
		"datacontenttype": "application/json", "ebaccount": "acc_sample000000000000000000",
		"data": data,
	}, nil
}

// jsonFields lists the JSON field names of a struct type, including embedded structs.
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	if t == nil {
		return out
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			for k := range jsonFields(f.Type) {
				out[k] = true
			}
			continue
		}
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// TestEventsFilter for GET /v1/routes/test-events.
type TestEventsFilter struct {
	Type      string
	AccountID string
	Limit     int
}

// TestEventRow is one picker row.
type TestEventRow struct {
	ID        string    `json:"id" doc:"Event ID (evt_…)" example:"evt_01m5czx69032gsbkp82n4ajd1w"`
	Type      string    `json:"type" doc:"Event type" example:"message.received"`
	Time      time.Time `json:"time" doc:"Event time" example:"2026-10-27T09:14:22Z"`
	AccountID string    `json:"accountId,omitempty" doc:"Account (acc_…)" example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Subject   string    `json:"subject,omitempty" doc:"Subject resource ID" example:"msg_01m5ch2yg09swc93g54trswp15"`
}

// TestEventList is the picker response.
type TestEventList struct {
	Data []TestEventRow `json:"data" doc:"Recent events, newest first."`
}

// ListTestEvents returns recent org events for the test picker.
func (r *Repo) ListTestEvents(ctx context.Context, f TestEventsFilter) (*TestEventList, error) {
	if err := tenancy.RequireRead(ctx, "routes"); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	// An account-bound API key sees only its own account's events (S10 §3), like GET /v1/events.
	if c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if f.AccountID != "" && f.AccountID != c.AccountID {
			return nil, errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
		}
		f.AccountID = c.AccountID
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := bson.M{"organizationId": c.OrganizationID}
	if f.Type != "" {
		q["type"] = f.Type
	}
	if f.AccountID != "" {
		q["accountId"] = f.AccountID
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	q["time"] = bson.M{"$gte": since}
	cur, err := r.DB.Collection("events").Find(ctx, q,
		options.Find().SetSort(bson.D{{Key: "time", Value: -1}}).SetLimit(int64(limit)).
			SetProjection(bson.M{"_id": 1, "type": 1, "time": 1, "accountId": 1, "data": 1}))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID        string    `bson:"_id"`
		Type      string    `bson:"type"`
		Time      time.Time `bson:"time"`
		AccountID string    `bson:"accountId"`
		Data      string    `bson:"data"` // exact JSON text of the envelope data (events.Writer)
	}
	if err := cur.All(ctx, &raw); err != nil {
		return nil, err
	}
	out := make([]TestEventRow, len(raw))
	for i, row := range raw {
		out[i] = TestEventRow{ID: row.ID, Type: row.Type, Time: row.Time, AccountID: row.AccountID, Subject: subjectOfData(row.Data)}
	}
	return &TestEventList{Data: out}, nil
}

// subjectOfData reads data.subject from the stored JSON text of an event ("" when absent or unparsable).
func subjectOfData(raw string) string {
	if raw == "" {
		return ""
	}
	var d struct {
		Subject string `json:"subject"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return ""
	}
	return d.Subject
}
