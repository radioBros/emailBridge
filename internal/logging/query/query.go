// Package query is the cluster side of the log system (S12 §8): it validates log queries, fans them out
// on eb.logs.query to every node's store (internal/logging/store answers), merges the answers newest
// first with a keyset cursor, reports nodes that missed the deadline, runs cluster-wide live tails, and
// serves the /v1/logs API.
package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

// Limits of the log API (S12 §8).
const (
	MaxSpan         = 31 * 24 * time.Hour
	MaxLimit        = 500
	DefaultLimit    = 50
	MaxQueryText    = 512
	DefaultDeadline = 5 * time.Second
)

var logIDRe = regexp.MustCompile(`^log_[0-9a-z]{26}$`)

// Query is a validated-on-use log query.
type Query struct {
	From, To       time.Time
	Categories     []logging.Category
	MinLevel       string
	OrganizationID string
	AccountID      string
	RequestID      string
	EventID        string
	DeliveryID     string
	OutboundID     string
	TraceID        string
	Q              string
	Limit          int
	Cursor         string
}

// Page is one page of merged results.
type Page struct {
	Records    []store.LogRecord
	NextCursor string   // "" when there is no next page
	Partial    []string // nodes that did not answer in time (or failed)
}

func validation(msg string) error { return errs.New(errs.CodeValidationFailed, msg) }

// checkCommon validates the parts shared by queries and tails.
func checkCommon(cats []logging.Category, level, q string) error {
	for _, c := range cats {
		if !c.Valid() {
			return validation(fmt.Sprintf("Unknown log category %q.", c))
		}
	}
	if level != "" {
		if _, err := logging.ParseLevel(level); err != nil {
			return validation(fmt.Sprintf("Unknown log level %q.", level))
		}
	}
	if len(q) > MaxQueryText {
		return validation(fmt.Sprintf("The search text exceeds %d characters.", MaxQueryText))
	}
	return nil
}

// checkRange validates a [from, to) range.
func checkRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() {
		return validation("Both from and to are required.")
	}
	if to.Before(from) {
		return validation("to must not be before from.")
	}
	if to.Sub(from) > MaxSpan {
		return errs.New(errs.CodeLogRangeTooLarge, "The time range exceeds 31 days.")
	}
	return nil
}

// filter validates q and returns the store filter (limit not yet set).
func (q *Query) filter() (store.Filter, int, error) {
	if err := checkRange(q.From, q.To); err != nil {
		return store.Filter{}, 0, err
	}
	if err := checkCommon(q.Categories, q.MinLevel, q.Q); err != nil {
		return store.Filter{}, 0, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return store.Filter{}, 0, validation(fmt.Sprintf("limit must be between 1 and %d.", MaxLimit))
	}
	f := store.Filter{From: q.From.UTC(), To: q.To.UTC(), Categories: q.Categories, Level: q.MinLevel, OrgID: q.OrganizationID,
		AccountID: q.AccountID, RequestID: q.RequestID, EventID: q.EventID, DeliveryID: q.DeliveryID, OutboundID: q.OutboundID,
		TraceID: q.TraceID, Q: q.Q}
	if q.Cursor != "" {
		p, err := DecodeCursor(q.Cursor)
		if err != nil {
			return store.Filter{}, 0, err
		}
		f.Before = p
	}
	return f, limit, nil
}

// EncodeCursor renders a keyset position as the opaque cursor (base64url of "time|id").
func EncodeCursor(p store.Position) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(p.Time, 10) + "|" + p.ID))
}

// DecodeCursor parses a cursor made by EncodeCursor.
func DecodeCursor(s string) (*store.Position, error) {
	bad := validation("The cursor is not valid.")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !logIDRe.MatchString(id) {
		return nil, bad
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, bad
	}
	return &store.Position{Time: t, ID: id}, nil
}

// Options configure a Service.
type Options struct {
	// Node is this node's ID.
	Node string
	// Bus fans queries out on eb.logs.query. Without it, Local answers alone (single process, tests).
	Bus *bus.Bus
	// Local is this node's responder (used when Bus is nil, and for its hub).
	Local *store.Node
	// Nodes lists the nodes expected to answer. Default: the eb-nodes registry plus this node. When the
	// expected set is unknown (external NATS and an empty registry), a fan-out waits for the full
	// deadline instead of returning after the first answer, so no node is dropped silently.
	Nodes func(ctx context.Context) ([]string, error)
	// Deadline is the fan-out deadline (default 5 s, never past the caller's context deadline).
	Deadline time.Duration
	// TailRate caps live tail per subscriber across the cluster (default store.TailRate).
	TailRate int
	// Log receives fan-out problems (system category).
	Log *slog.Logger
}

// Service runs log queries across the cluster.
type Service struct {
	o Options
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Bus == nil && o.Local == nil {
		return nil, errors.New("logquery: needs a bus or a local node")
	}
	if o.Node == "" && o.Local != nil && o.Local.Store != nil {
		o.Node = o.Local.Store.Node()
	}
	if o.Deadline <= 0 {
		o.Deadline = DefaultDeadline
	}
	if o.TailRate <= 0 {
		o.TailRate = store.TailRate
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o}, nil
}

// expected returns the nodes that should answer and whether that set is complete. It is complete when
// given by Options.Nodes, read from a non-empty registry, or when the bus is embedded (single node).
func (s *Service) expected(ctx context.Context) ([]string, bool) {
	var nodes []string
	var err error
	if s.o.Nodes != nil {
		nodes, err = s.o.Nodes(ctx)
	} else {
		nodes, err = s.o.Bus.NodeIDs(ctx)
	}
	if err != nil {
		s.o.Log.LogAttrs(ctx, slog.LevelWarn, "node registry unavailable for log query", slog.Any("err", err))
		nodes = nil
	}
	known := err == nil && (s.o.Nodes != nil || len(nodes) > 0 || s.o.Bus.Embedded())
	if s.o.Node != "" && !slices.Contains(nodes, s.o.Node) {
		nodes = append(nodes, s.o.Node)
	}
	slices.Sort(nodes)
	return nodes, known
}

// scatter sends req to every node and returns the successful replies and the nodes that did not answer
// (or failed). A filter every node rejects is a validation error.
func (s *Service) scatter(ctx context.Context, req store.Request) ([]store.Reply, []string, error) {
	deadline := s.o.Deadline
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < deadline {
			deadline = rem
		}
	}
	if deadline <= 0 {
		return nil, nil, errs.New(errs.CodeTimeout, "The request deadline passed before the log query started.")
	}
	req.TimeoutMs = deadline.Milliseconds()
	if req.TimeoutMs == 0 {
		req.TimeoutMs = 1
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("logquery: encode request: %w", err)
	}
	var raws [][]byte
	var expected []string
	if s.o.Bus == nil {
		raws = [][]byte{s.o.Local.Handle(ctx, data)}
		expected = []string{s.o.Local.Store.Node()}
	} else {
		var known bool
		expected, known = s.expected(ctx)
		expect := len(expected)
		if !known {
			expect = 0 // unknown membership: collect until the deadline
		}
		raws, err = s.o.Bus.ScatterGather(ctx, bus.LogsQuerySubject, data, expect, deadline)
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.CodeServiceUnavailable, "logquery.scatter")
		}
	}
	answered := map[string]bool{}
	var reps []store.Reply
	var badFilter bool
	for _, raw := range raws {
		var rep store.Reply
		if err := json.Unmarshal(raw, &rep); err != nil || rep.Node == "" || answered[rep.Node] {
			continue
		}
		switch rep.Error {
		case "":
			answered[rep.Node] = true
			reps = append(reps, rep)
		case store.ReplyBadFilter:
			badFilter = true
		default:
			s.o.Log.LogAttrs(ctx, slog.LevelWarn, "node failed a log query", slog.String("node", rep.Node), slog.String("op", req.Op),
				slog.String("error", rep.Error))
		}
	}
	if badFilter && len(reps) == 0 {
		return nil, nil, validation("The log filter cannot be executed (check the search text).")
	}
	var partial []string
	for _, n := range expected {
		if !answered[n] {
			partial = append(partial, n)
		}
	}
	return reps, partial, nil
}

// Search runs q on every node and merges the answers newest first.
func (s *Service) Search(ctx context.Context, q Query) (*Page, error) {
	f, limit, err := q.filter()
	if err != nil {
		return nil, err
	}
	f.Limit = limit + 1 // one extra record tells whether a next page exists
	reps, partial, err := s.scatter(ctx, store.Request{Op: store.OpQuery, Filter: &f})
	if err != nil {
		return nil, err
	}
	return merge(reps, partial, limit), nil
}

// merge combines node answers. A node that truncated its answer to fit the payload bounds the page: no
// record older than its last returned record may be included, or that node's next records would be
// skipped; the cursor then continues from the page end.
func merge(reps []store.Reply, partial []string, limit int) *Page {
	seen := map[string]bool{}
	var all []store.LogRecord
	var cut *store.Position
	for _, rep := range reps {
		var last *store.Position
		for _, raw := range rep.Records {
			var r store.LogRecord
			if err := json.Unmarshal(raw, &r); err != nil || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			all = append(all, r)
			p := r.Pos()
			last = &p
		}
		if rep.Truncated && last != nil && (cut == nil || cut.Before(*last)) {
			cut = last
		}
	}
	store.SortNewestFirst(all)
	more := false
	if cut != nil {
		n := len(all)
		for i, r := range all {
			if r.Pos().Before(*cut) {
				n = i
				break
			}
		}
		all = all[:n]
		more = true
	}
	if len(all) > limit {
		all, more = all[:limit], true
	}
	page := &Page{Records: all, Partial: partial}
	if page.Records == nil {
		page.Records = []store.LogRecord{}
	}
	if more && len(all) > 0 {
		page.NextCursor = EncodeCursor(all[len(all)-1].Pos())
	}
	return page
}

// Get looks a record up on every node in the day file its ID points to. It returns nil (and the nodes
// that did not answer) when no node has it.
func (s *Service) Get(ctx context.Context, id string) (*store.LogRecord, []string, error) {
	if !logIDRe.MatchString(id) {
		return nil, nil, validation("The log ID is not valid.")
	}
	if _, err := store.IDTime(id); err != nil {
		return nil, nil, validation("The log ID is not valid.")
	}
	reps, partial, err := s.scatter(ctx, store.Request{Op: store.OpGet, ID: id})
	if err != nil {
		return nil, nil, err
	}
	for _, rep := range reps {
		if rep.Record != nil {
			return rep.Record, partial, nil
		}
	}
	return nil, partial, nil
}

// Stats are cluster-wide counts and disk usage.
type Stats struct {
	ByLevel    map[string]int64
	ByCategory map[string]int64
	// Disk is bytes per category and node.
	Disk    map[string]map[string]int64
	Partial []string
}

// Stats counts records by level and category in [from, to) (only orgID's records when orgID is set)
// and collects disk usage per category and node.
func (s *Service) Stats(ctx context.Context, from, to time.Time, orgID string) (*Stats, error) {
	if err := checkRange(from, to); err != nil {
		return nil, err
	}
	f := store.Filter{From: from.UTC(), To: to.UTC(), OrgID: orgID}
	reps, partial, err := s.scatter(ctx, store.Request{Op: store.OpStats, Filter: &f})
	if err != nil {
		return nil, err
	}
	out := &Stats{ByLevel: map[string]int64{}, ByCategory: map[string]int64{}, Disk: map[string]map[string]int64{}, Partial: partial}
	for _, rep := range reps {
		if rep.Stats == nil {
			continue
		}
		for k, v := range rep.Stats.ByLevel {
			out.ByLevel[k] += v
		}
		for k, v := range rep.Stats.ByCategory {
			out.ByCategory[k] += v
		}
		for c, v := range rep.Stats.Disk {
			if out.Disk[c] == nil {
				out.Disk[c] = map[string]int64{}
			}
			out.Disk[c][rep.Node] += v
		}
	}
	return out, nil
}

// Erase deletes an account's log records on every node (S11 §5.1) and returns the total deleted and
// the nodes that did not confirm (the erasure job retries until none is left).
func (s *Service) Erase(ctx context.Context, accountID string) (int64, []string, error) {
	if strings.TrimSpace(accountID) == "" {
		return 0, nil, validation("accountId is required.")
	}
	reps, partial, err := s.scatter(ctx, store.Request{Op: store.OpErase, AccountID: accountID})
	if err != nil {
		return 0, nil, err
	}
	var n int64
	for _, rep := range reps {
		n += rep.Erased
	}
	return n, partial, nil
}

// Each runs q page by page (newest first) and calls fn for every page until the results end or fn
// fails. q.Limit is the page size (default MaxLimit).
func (s *Service) Each(ctx context.Context, q Query, fn func(*Page) error) error {
	if q.Limit == 0 {
		q.Limit = MaxLimit
	}
	for {
		p, err := s.Search(ctx, q)
		if err != nil {
			return err
		}
		if err := fn(p); err != nil {
			return err
		}
		if p.NextCursor == "" {
			return nil
		}
		q.Cursor = p.NextCursor
	}
}

// Export writes every record matching q to w as NDJSON (newest first) and returns the number written and
// the nodes that were missing from any page.
func (s *Service) Export(ctx context.Context, q Query, w io.Writer) (int64, []string, error) {
	var n int64
	missing := map[string]bool{}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	err := s.Each(ctx, q, func(p *Page) error {
		for _, node := range p.Partial {
			missing[node] = true
		}
		buf.Reset()
		for i := range p.Records {
			if err := enc.Encode(&p.Records[i]); err != nil {
				return fmt.Errorf("logquery: encode: %w", err)
			}
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("logquery: export write: %w", err)
		}
		n += int64(len(p.Records))
		return nil
	})
	var partial []string
	for node := range missing {
		partial = append(partial, node)
	}
	slices.Sort(partial)
	return n, partial, err
}
