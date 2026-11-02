package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/radiobros/emailbridge/internal/logging"
)

var base = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type capture struct {
	mu   sync.Mutex
	msgs []string
	buf  bytes.Buffer
}

func (c *capture) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&lockedWriter{c}, nil))
}

type lockedWriter struct{ c *capture }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	w.c.msgs = append(w.c.msgs, string(p))
	return w.c.buf.Write(p)
}

func (c *capture) count(sub string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

func openTest(t *testing.T, mut func(*Options)) *Store {
	t.Helper()
	o := Options{Dir: t.TempDir(), Node: "n1", SweepEvery: -1, Now: func() time.Time { return base }}
	if mut != nil {
		mut(&o)
	}
	s, err := Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newID(t time.Time, seq int) string {
	var e [10]byte
	e[8], e[9] = byte(seq>>8), byte(seq)
	return "log_" + strings.ToLower(ulid.MustNew(ulid.Timestamp(t), bytes.NewReader(e[:])).String())
}

func rec(t time.Time, seq int, cat logging.Category, level slog.Level, msg string, mut func(*logging.Record)) logging.Record {
	r := logging.Record{ID: newID(t, seq), Time: t, Level: level, LevelName: logging.LevelName(level), Category: cat,
		Component: "test", Node: "n1", Role: "api", Msg: msg}
	if mut != nil {
		mut(&r)
	}
	return r
}

func write(t *testing.T, s *Store, recs ...logging.Record) {
	t.Helper()
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
}

func rangeFilter() Filter {
	return Filter{From: base.Add(-10 * 24 * time.Hour), To: base.Add(24 * time.Hour), Limit: 100}
}

func TestOpenValidation(t *testing.T) {
	ctx := context.Background()
	for _, o := range []Options{{Dir: "", Node: "n1"}, {Dir: t.TempDir(), Node: "../x"}, {Dir: t.TempDir(), Node: ""},
		{Dir: t.TempDir() + "?x", Node: "n1"}, {Dir: t.TempDir(), Node: "a/b"}} {
		if _, err := Open(ctx, o); err == nil {
			t.Errorf("Open(%+v) must fail", o)
		}
	}
	s, err := Open(ctx, Options{Dir: t.TempDir(), Node: "node-1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != SinkName || s.Node() != "node-1" {
		t.Fatal("identity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal("double close")
	}
	if err := s.Write(ctx, []logging.Record{rec(base, 1, logging.CatSystem, logging.LevelInfo, "x", nil)}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := s.Write(ctx, nil); err != nil {
		t.Fatal("empty batch")
	}
}

func TestWriteLayoutSchemaAndPragmas(t *testing.T) {
	s := openTest(t, nil)
	write(t, s,
		rec(base, 1, logging.CatSync, logging.LevelInfo, "idle started", nil),
		rec(base.Add(-24*time.Hour), 2, logging.CatSync, logging.LevelWarn, "yesterday", nil),
		rec(base, 3, logging.Category("bogus"), logging.LevelInfo, "uncategorised", nil),
	)
	for _, p := range []string{"n1/sync/2026-10-20.db", "n1/sync/2026-10-19.db", "n1/system/2026-10-20.db"} {
		if _, err := os.Stat(filepath.Join(s.o.Dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	w := s.writers[fileKey{cat: logging.CatSync, day: "2026-10-20"}]
	if w == nil {
		t.Fatal("writer not pooled")
	}
	var mode string
	var av, sync int
	if err := w.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode %q %v", mode, err)
	}
	if err := w.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&av); err != nil || av != 2 {
		t.Errorf("auto_vacuum %d %v", av, err)
	}
	if err := w.db.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil || sync != 1 {
		t.Errorf("synchronous %d %v", sync, err)
	}
	rows, err := w.db.Query(`SELECT name FROM sqlite_master WHERE type IN ('index','trigger','table')`)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names[n] = true
	}
	_ = rows.Close()
	for _, n := range []string{"records", "records_fts", "records_time", "records_level_time", "records_org_time", "records_account_time",
		"records_request", "records_trace", "records_event", "records_delivery", "records_outbound", "records_ai", "records_ad", "records_au"} {
		if !names[n] {
			t.Errorf("schema object %s missing", n)
		}
	}
	// the account index serves account queries
	var plan strings.Builder
	prow, err := w.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM records WHERE accountId = ? AND time >= ? ORDER BY time DESC`, "acc", 0)
	if err != nil {
		t.Fatal(err)
	}
	for prow.Next() {
		var a, b, c int
		var d string
		_ = prow.Scan(&a, &b, &c, &d)
		plan.WriteString(d)
	}
	_ = prow.Close()
	if !strings.Contains(plan.String(), "records_account_time") {
		t.Errorf("plan does not use the account index: %s", plan.String())
	}
	// duplicate IDs are ignored (a re-delivered batch never duplicates records)
	write(t, s, rec(base, 1, logging.CatSync, logging.LevelInfo, "idle started", nil))
	got, err := s.Search(context.Background(), rangeFilter())
	if err != nil || len(got) != 3 {
		t.Fatalf("search all: %d %v", len(got), err)
	}
	if got[0].Category != "system" || got[0].Msg != "uncategorised" {
		t.Errorf("unknown category must land in system: %+v", got[0])
	}
}

func TestRoundTripFields(t *testing.T) {
	s := openTest(t, nil)
	write(t, s, rec(base, 1, logging.CatCrash, logging.LevelError, "panic recovered", func(r *logging.Record) {
		r.IDs = logging.IDs{RequestID: "req_1", TraceID: "tr", SpanID: "sp", OrganizationID: "org_1", AccountID: "acc_1",
			EventID: "evt_1", DeliveryID: "dlv_1", OutboundID: "out_1", Actor: "usr_1", JobID: "job_1"}
		r.Kind, r.Code, r.Stack = "internal", "internal_error", "goroutine 1"
		r.Attrs = map[string]any{"big": int64(1) << 60, "nested": map[string]any{"k": "v"}}
	}))
	got, err := s.Search(context.Background(), rangeFilter())
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	r := got[0]
	if str(r.RequestID) != "req_1" || str(r.TraceID) != "tr" || str(r.SpanID) != "sp" || str(r.OrganizationID) != "org_1" ||
		str(r.AccountID) != "acc_1" || str(r.EventID) != "evt_1" || str(r.DeliveryID) != "dlv_1" || str(r.OutboundID) != "out_1" ||
		str(r.Actor) != "usr_1" || str(r.JobID) != "job_1" || str(r.Kind) != "internal" || str(r.Code) != "internal_error" ||
		str(r.Stack) != "goroutine 1" || r.Level != "error" || r.Component != "test" || r.Node != "n1" || r.Role != "api" ||
		r.Time != base.UnixNano() {
		t.Fatalf("round trip %+v", r)
	}
	if n, ok := r.Attrs["big"].(json.Number); !ok || n.String() != "1152921504606846976" {
		t.Errorf("int64 attribute lost precision: %#v", r.Attrs["big"])
	}
	b, _ := json.Marshal(&r)
	for _, k := range []string{`"time":`, `"organizationId":"org_1"`, `"attrs":`} {
		if !bytes.Contains(b, []byte(k)) {
			t.Errorf("json lacks %s: %s", k, b)
		}
	}
	// absent IDs render as null
	write(t, s, rec(base.Add(time.Second), 2, logging.CatSystem, logging.LevelInfo, "plain", nil))
	got, _ = s.Search(context.Background(), rangeFilter())
	b, _ = json.Marshal(&got[0])
	if !bytes.Contains(b, []byte(`"accountId":null`)) || bytes.Contains(b, []byte(`"attrs"`)) {
		t.Errorf("null rendering: %s", b)
	}
	// attributes that cannot be marshalled are replaced by a marker
	if v := attrsJSON(map[string]any{"ch": make(chan int)}); !strings.Contains(v.(string), "_attrsError") {
		t.Errorf("marker %v", v)
	}
}

func seed(t *testing.T, s *Store) {
	t.Helper()
	var recs []logging.Record
	cats := []logging.Category{logging.CatAPI, logging.CatSync, logging.CatSend}
	levels := []slog.Level{logging.LevelDebug, logging.LevelInfo, logging.LevelWarn, logging.LevelError}
	for i := 0; i < 120; i++ {
		ts := base.Add(-time.Duration(i) * time.Hour) // spans 5 days
		recs = append(recs, rec(ts, i, cats[i%3], levels[i%4], fmt.Sprintf("message number %d", i), func(r *logging.Record) {
			r.IDs.OrganizationID = []string{"org_a", "org_b", ""}[i%3]
			r.IDs.AccountID = fmt.Sprintf("acc_%d", i%5)
			r.IDs.RequestID = fmt.Sprintf("req_%d", i)
			if i%10 == 0 {
				r.Attrs = map[string]any{"remote": "smtp.example.net", "status": "connection refused"}
			}
		}))
	}
	write(t, s, recs...)
}

func TestSearchFilters(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	ctx := context.Background()
	count := func(f Filter) int {
		t.Helper()
		got, err := s.Search(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(got); i++ {
			if !got[i].Pos().Before(got[i-1].Pos()) {
				t.Fatal("not newest first")
			}
		}
		return len(got)
	}
	f := rangeFilter()
	f.Limit = 1000
	if n := count(f); n != 120 {
		t.Errorf("all %d", n)
	}
	cases := []struct {
		name string
		mut  func(*Filter)
		want int
	}{
		{"category", func(f *Filter) { f.Categories = []logging.Category{logging.CatAPI} }, 40},
		{"two categories", func(f *Filter) { f.Categories = []logging.Category{logging.CatAPI, logging.CatSync, logging.CatAPI} }, 80},
		{"unknown category ignored", func(f *Filter) { f.Categories = []logging.Category{"nope"} }, 0},
		{"min level warn", func(f *Filter) { f.Level = "warn" }, 60},
		{"min level error", func(f *Filter) { f.Level = "error" }, 30},
		{"org", func(f *Filter) { f.OrgID = "org_a" }, 40},
		{"account", func(f *Filter) { f.AccountID = "acc_1" }, 24},
		{"request", func(f *Filter) { f.RequestID = "req_7" }, 1},
		{"event none", func(f *Filter) { f.EventID = "evt_x" }, 0},
		{"delivery none", func(f *Filter) { f.DeliveryID = "x" }, 0},
		{"outbound none", func(f *Filter) { f.OutboundID = "x" }, 0},
		{"trace none", func(f *Filter) { f.TraceID = "x" }, 0},
		{"fts msg phrase", func(f *Filter) { f.Q = "number 7" }, 11}, // 7, 70..79 — prefix on the last token
		{"fts attrs", func(f *Filter) { f.Q = "refused" }, 12},
		{"fts prefix", func(f *Filter) { f.Q = "conn" }, 12},
		{"fts case", func(f *Filter) { f.Q = "MESSAGE" }, 120},
		{"fts hostile", func(f *Filter) { f.Q = `" OR 1 --` }, 0},
		{"fts operators are text", func(f *Filter) { f.Q = `message NOT number` }, 0},
		{"fts column filter is text", func(f *Filter) { f.Q = `msg:message` }, 0},
		{"fts only punctuation", func(f *Filter) { f.Q = `"*(^)` }, 120},
		{"time range", func(f *Filter) { f.From, f.To = base.Add(-9*time.Hour), base.Add(time.Nanosecond) }, 10},
		{"combined", func(f *Filter) {
			f.Categories = []logging.Category{logging.CatAPI}
			f.Level = "info"
			f.OrgID = "org_a"
		}, 30},
	}
	for _, c := range cases {
		f := rangeFilter()
		f.Limit = 1000
		c.mut(&f)
		if n := count(f); n != c.want {
			t.Errorf("%s: %d, want %d", c.name, n, c.want)
		}
	}
	// bad filters
	for _, f := range []Filter{{Limit: 10}, {From: base, To: base.Add(70 * 24 * time.Hour), Limit: 10},
		{From: base, To: base.Add(time.Hour), Limit: 0}, {From: base, To: base.Add(time.Hour), Limit: 10, Level: "loud"}} {
		if _, err := s.Search(ctx, f); !errors.Is(err, ErrBadFilter) {
			t.Errorf("filter %+v: %v", f, err)
		}
	}
	if got, err := s.Search(ctx, Filter{From: base, To: base, Limit: 5}); err != nil || len(got) != 0 {
		t.Errorf("empty range %v %v", got, err)
	}
}

func TestSearchKeysetPagingIsStable(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	// records sharing one timestamp page by id
	var same []logging.Record
	for i := 0; i < 7; i++ {
		same = append(same, rec(base.Add(-30*time.Minute), 500+i, logging.CatJob, logging.LevelInfo, "same time", nil))
	}
	write(t, s, same...)
	ctx := context.Background()
	f := rangeFilter()
	f.Limit = 7
	seen := map[string]bool{}
	var last *Position
	pages := 0
	for {
		f.Before = last
		got, err := s.Search(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range got {
			if seen[r.ID] {
				t.Fatalf("duplicate %s across pages", r.ID)
			}
			if last != nil && !r.Pos().Before(*last) {
				t.Fatal("page not after cursor")
			}
			seen[r.ID] = true
		}
		pages++
		if len(got) < f.Limit {
			break
		}
		p := got[len(got)-1].Pos()
		last = &p
		// a new record arriving between pages does not shift later pages
		write(t, s, rec(base.Add(time.Duration(pages)*time.Second), 900+pages, logging.CatAPI, logging.LevelInfo, "late", nil))
	}
	if len(seen) != 127 || pages != 19 {
		t.Fatalf("paged %d records in %d pages", len(seen), pages)
	}
}

func TestFTSQueryEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"hello":         `"hello"*`,
		`say "hi"`:      `"say ""hi"""*`,
		`" OR 1 --`:     `""" OR 1 --"*`,
		"  spaced  ":    `"spaced"*`,
		"àccent":        `"àccent"*`,
		"msg:x NEAR(y)": `"msg:x NEAR(y)"*`,
	} {
		got, ok := FTSQuery(in)
		if !ok || got != want {
			t.Errorf("FTSQuery(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "   ", `"`, "*", "()--"} {
		if _, ok := FTSQuery(in); ok {
			t.Errorf("FTSQuery(%q) must be dropped", in)
		}
	}
}

func TestGetAndIDTime(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	ctx := context.Background()
	id := newID(base.Add(-50*time.Hour), 50)
	r, err := s.Get(ctx, id)
	if err != nil || r == nil || r.ID != id || r.Msg != "message number 50" {
		t.Fatalf("get %+v %v", r, err)
	}
	if r, err := s.Get(ctx, newID(base.Add(-50*time.Hour), 9999)); err != nil || r != nil {
		t.Fatalf("missing id: %v %v", r, err)
	}
	for _, bad := range []string{"evt_x", "log_notaulid", "log_"} {
		if _, err := s.Get(ctx, bad); !errors.Is(err, ErrBadFilter) {
			t.Errorf("Get(%q): %v", bad, err)
		}
	}
	tm, err := IDTime(id)
	if err != nil || !tm.Equal(base.Add(-50*time.Hour)) {
		t.Fatalf("IDTime %v %v", tm, err)
	}
}

func TestStats(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	ctx := context.Background()
	st, err := s.Stats(ctx, rangeFilter())
	if err != nil {
		t.Fatal(err)
	}
	if st.ByLevel["debug"] != 30 || st.ByLevel["error"] != 30 || st.ByCategory["api"] != 40 || st.ByCategory["send"] != 40 {
		t.Fatalf("stats %+v", st)
	}
	if st.Disk["api"] <= 0 || st.Disk["sync"] <= 0 || st.Disk["audit"] != 0 {
		t.Fatalf("disk %+v", st.Disk)
	}
	f := rangeFilter()
	f.OrgID = "org_b"
	st, err = s.Stats(ctx, f)
	if err != nil || st.ByCategory["sync"] != 40 || st.ByCategory["api"] != 0 {
		t.Fatalf("org stats %+v %v", st, err)
	}
	f.Q = "refused"
	if _, err := s.Stats(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stats(ctx, Filter{}); !errors.Is(err, ErrBadFilter) {
		t.Fatal("stats without range")
	}
	if _, err := s.Stats(ctx, Filter{From: base, To: base.Add(time.Hour), Level: "x"}); !errors.Is(err, ErrBadFilter) {
		t.Fatal("stats bad level")
	}
}

func TestEraseKeepsComplianceCategories(t *testing.T) {
	s := openTest(t, nil)
	ctx := context.Background()
	acc := func(r *logging.Record) { r.IDs.AccountID = "acc_gone"; r.Attrs = map[string]any{"who": "gone person"} }
	write(t, s,
		rec(base, 1, logging.CatSync, logging.LevelInfo, "sync", acc),
		rec(base.Add(-48*time.Hour), 2, logging.CatProtocol, logging.LevelDebug, "proto", acc),
		rec(base, 3, logging.CatAudit, logging.LevelInfo, "audit", acc),
		rec(base, 4, logging.CatSecurity, logging.LevelInfo, "security", acc),
		rec(base, 5, logging.CatCrash, logging.LevelError, "crash", acc),
		rec(base, 6, logging.CatSync, logging.LevelInfo, "other account", func(r *logging.Record) { r.IDs.AccountID = "acc_keep" }),
	)
	s.CloseIdle() // erasure must also work on closed day files
	n, err := s.Erase(ctx, "acc_gone")
	if err != nil || n != 2 {
		t.Fatalf("erased %d %v", n, err)
	}
	f := rangeFilter()
	f.AccountID = "acc_gone"
	got, _ := s.Search(ctx, f)
	cats := map[string]bool{}
	for _, r := range got {
		cats[r.Category] = true
	}
	if len(got) != 3 || !cats["audit"] || !cats["security"] || !cats["crash"] {
		t.Fatalf("kept %v", cats)
	}
	// the FTS index forgot the erased rows too
	f = rangeFilter()
	f.Q = "gone person"
	f.Categories = []logging.Category{logging.CatSync, logging.CatProtocol}
	if got, _ := s.Search(ctx, f); len(got) != 0 {
		t.Fatalf("fts still finds erased rows: %d", len(got))
	}
	if n, err := s.Erase(ctx, "acc_gone"); err != nil || n != 0 {
		t.Fatalf("idempotent erase %d %v", n, err)
	}
	if _, err := s.Erase(ctx, " "); err == nil {
		t.Fatal("empty account")
	}
}

func TestExport(t *testing.T) {
	s := openTest(t, nil)
	var recs []logging.Record
	for i := 0; i < 1203; i++ {
		recs = append(recs, rec(base.Add(-time.Duration(i)*time.Second), i, logging.CatEvent, logging.LevelInfo, "event emitted", nil))
	}
	write(t, s, recs...)
	write(t, s, rec(base.Add(-30*time.Hour), 1, logging.CatEvent, logging.LevelInfo, "yesterday", nil))
	var buf bytes.Buffer
	n, err := s.Export(context.Background(), rangeFilter(), &buf)
	if err != nil || n != 1204 {
		t.Fatalf("export %d %v", n, err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1204 {
		t.Fatalf("lines %d", len(lines))
	}
	var first LogRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Time != base.UnixNano() {
		t.Fatalf("first line %s %v", lines[0], err)
	}
	buf.Reset()
	n, err = s.ExportDay(context.Background(), base.Add(-30*time.Hour), []logging.Category{logging.CatEvent}, &buf)
	if err != nil || n != 1 {
		t.Fatalf("export day %d %v", n, err)
	}
	if _, err := s.Export(context.Background(), Filter{}, &buf); err == nil {
		t.Fatal("export without range")
	}
	if _, err := s.Export(context.Background(), rangeFilter(), failWriter{}); err == nil {
		t.Fatal("write error must surface")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestIdleCloseAndLRU(t *testing.T) {
	now := base
	var mu sync.Mutex
	s := openTest(t, func(o *Options) {
		o.IdleClose = time.Minute
		o.MaxOpen = 2
		o.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	write(t, s, rec(base, 1, logging.CatAPI, logging.LevelInfo, "a", nil), rec(base, 2, logging.CatSync, logging.LevelInfo, "b", nil))
	if s.OpenFiles() != 2 {
		t.Fatalf("open %d", s.OpenFiles())
	}
	write(t, s, rec(base, 3, logging.CatSend, logging.LevelInfo, "c", nil))
	if s.OpenFiles() != 2 {
		t.Fatalf("LRU must cap open files: %d", s.OpenFiles())
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	s.CloseIdle()
	if s.OpenFiles() != 0 {
		t.Fatalf("idle files must close: %d", s.OpenFiles())
	}
	// reading a closed file works and does not reopen a writer
	got, err := s.Search(context.Background(), rangeFilter())
	if err != nil || len(got) != 3 || s.OpenFiles() != 0 {
		t.Fatalf("read after idle close: %d %v open=%d", len(got), err, s.OpenFiles())
	}
	// reader handles are cached (bounded by MaxReaders) and idle-closed like writers
	if n := s.OpenReaders(); n != 3 {
		t.Fatalf("cached readers %d", n)
	}
	s.o.MaxReaders = 2
	write(t, s, rec(base, 4, logging.CatJob, logging.LevelInfo, "d", nil))
	for _, c := range []logging.Category{logging.CatAPI, logging.CatSync, logging.CatSend, logging.CatJob} {
		_, release, ok, err := s.openReader(c, "2026-10-20")
		if err != nil || !ok {
			t.Fatal(c, err)
		}
		release()
	}
	if n := s.OpenReaders(); n != 2 {
		t.Fatalf("reader LRU keeps %d idle handles, want 2", n)
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	s.CloseIdle()
	if s.OpenReaders() != 0 || s.OpenFiles() != 0 {
		t.Fatalf("idle readers %d writers %d", s.OpenReaders(), s.OpenFiles())
	}
	// a reader still in use when the store closes is closed by its release
	_, release, ok, err := s.openReader(logging.CatAPI, "2026-10-20")
	if err != nil || !ok {
		t.Fatal(err)
	}
	_ = s.Close()
	release()
	if _, _, _, err := s.openReader(logging.CatAPI, "2026-10-20"); !errors.Is(err, ErrClosed) {
		t.Fatalf("reader after close: %v", err)
	}
}

func TestBackgroundLoopsRun(t *testing.T) {
	c := &capture{}
	now := time.Now()
	s := openTest(t, func(o *Options) {
		o.IdleClose = 20 * time.Millisecond
		o.SweepEvery = 30 * time.Millisecond
		o.Now = time.Now
		o.Log = c.logger()
		o.Settings = func() logging.Settings {
			st := logging.DefaultSettings()
			cs := st.Categories[logging.CatAPI]
			cs.Retention = 24 * time.Hour
			st.Categories[logging.CatAPI] = cs
			return st
		}
	})
	write(t, s, rec(now.Add(-72*time.Hour), 1, logging.CatAPI, logging.LevelInfo, "old", nil))
	deadline := time.Now().Add(5 * time.Second)
	for c.count("log day file deleted") == 0 || s.OpenFiles() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("background loops did not run: deleted=%d open=%d", c.count("log day file deleted"), s.OpenFiles())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReaderNeverCreatesFilesAndToleratesFreshOnes(t *testing.T) {
	s := openTest(t, nil)
	ctx := context.Background()
	// a day file whose first write has not created the schema yet reads as empty
	dir := filepath.Join(s.root, "api")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-20.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Search(ctx, rangeFilter()); err != nil || len(got) != 0 {
		t.Fatalf("fresh file: %v %v", got, err)
	}
	if _, err := s.Stats(ctx, rangeFilter()); err != nil {
		t.Fatalf("fresh file stats: %v", err)
	}
	// queries never create day files
	if _, err := s.Get(ctx, newID(base.Add(-72*time.Hour), 1)); err != nil {
		t.Fatal(err)
	}
	if files, _ := s.days(logging.CatSync); len(files) != 0 {
		t.Fatalf("a query created %v", files)
	}
	if got := readerDSN(filepath.Join("logs", "100%", "n1", "x.db")); got != "file:logs/100%25/n1/x.db"+readerPragmas {
		t.Fatalf("dsn %s", got)
	}
}

func TestReadsDoNotWaitForWrites(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	s.mu.Lock() // a long batch write in progress
	done := make(chan error, 1)
	go func() {
		_, err := s.Search(context.Background(), rangeFilter())
		if err == nil {
			_, err = s.Stats(context.Background(), rangeFilter())
		}
		done <- err
	}()
	select {
	case err := <-done:
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second): // a query needing the lock would block until it is released
		s.mu.Unlock()
		t.Fatal("queries must not wait for the write lock")
	}
}
