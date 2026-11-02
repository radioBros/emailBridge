package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type memSink struct {
	mu      sync.Mutex
	recs    []Record
	block   chan struct{} // when non-nil, Write waits on it
	failErr error
}

func (m *memSink) Name() string { return "mem" }
func (m *memSink) Write(_ context.Context, b []Record) error {
	m.mu.Lock()
	block := m.block
	m.mu.Unlock()
	if block != nil {
		<-block
	}
	if m.failErr != nil {
		return m.failErr
	}
	m.mu.Lock()
	m.recs = append(m.recs, b...)
	m.mu.Unlock()
	return nil
}
func (m *memSink) all() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.recs...)
}

func newSys(t *testing.T, o Options) (*System, *memSink) {
	t.Helper()
	if o.Stdout == nil {
		o.Stdout = &bytes.Buffer{}
	}
	if o.Fallback == nil {
		o.Fallback = &bytes.Buffer{}
	}
	s := New(o)
	m := &memSink{}
	s.AddSink(m)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, m
}

func flush(t *testing.T, s *System) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCategoriesAndLevels(t *testing.T) {
	if len(Categories) != nCategories || len(EvictionOrder()) != nCategories {
		t.Fatalf("category count mismatch: %d %d %d", len(Categories), len(EvictionOrder()), nCategories)
	}
	for _, c := range Categories {
		if !c.Valid() {
			t.Errorf("%s not valid", c)
		}
		if _, ok := DefaultSettings().Categories[c]; !ok {
			t.Errorf("no default settings for %s", c)
		}
	}
	if Category("nope").Valid() {
		t.Error("unknown category valid")
	}
	for _, n := range []string{"trace", "debug", "info", "warn", "error", "fatal"} {
		l, err := ParseLevel(n)
		if err != nil || LevelName(l) != n {
			t.Errorf("level %s: %v %s", n, err, LevelName(l))
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("bad level accepted")
	}
	if l, _ := ParseLevel("WARNING"); l != LevelWarn {
		t.Error("warning alias")
	}
}

func TestRecordFieldsAndContextIDs(t *testing.T) {
	s, m := newSys(t, Options{Node: "n1", Role: "all", Classifier: func(err error) (string, string) {
		return "temporary", "mail_timeout"
	}})
	ctx := With(context.Background(), func(id *IDs) { id.RequestID = "req_1"; id.AccountID = "acc_1" })
	ctx = With(ctx, func(id *IDs) { id.TraceID = "t1" })
	log := s.For(CatSync, "sync.idle").With("mailbox", "INBOX").WithGroup("imap")
	log.InfoContext(ctx, "idle restarted", "uid", 42, "err", errors.New("boom"))
	log.WarnContext(ctx, "explicit", slog.String("kind", "should-not-move-under-group"))
	s.For(CatSend, "send").Error("smtp failed", "kind", "permanent", "code", "smtp_permanent")
	flush(t, s)
	recs := m.all()
	if len(recs) != 3 {
		t.Fatalf("got %d records", len(recs))
	}
	r := recs[0]
	if r.Category != CatSync || r.Component != "sync.idle" || r.Node != "n1" || r.Role != "all" || r.LevelName != "info" ||
		r.Msg != "idle restarted" || !strings.HasPrefix(r.ID, "log_") || len(r.ID) != 30 {
		t.Fatalf("bad record %+v", r)
	}
	if r.IDs.RequestID != "req_1" || r.IDs.AccountID != "acc_1" || r.IDs.TraceID != "t1" {
		t.Fatalf("ids %+v", r.IDs)
	}
	if r.Attrs["mailbox"] != "INBOX" || r.Attrs["imap.uid"] != int64(42) || r.Attrs["imap.err"] != "boom" {
		t.Fatalf("attrs %#v", r.Attrs)
	}
	if recs[1].Attrs["imap.kind"] == nil || recs[1].Kind != "" {
		t.Fatalf("grouped kind must stay an attr: %+v", recs[1])
	}
	if recs[2].Kind != "permanent" || recs[2].Code != "smtp_permanent" {
		t.Fatalf("kind/code %+v", recs[2])
	}
	// classifier fills kind/code from a top-level err
	s.For(CatSync, "x").Error("e", "err", errors.New("x"))
	flush(t, s)
	last := m.all()[3]
	if last.Kind != "temporary" || last.Code != "mail_timeout" {
		t.Fatalf("classifier %+v", last)
	}
	if IDsFrom(nil).RequestID != "" { //nolint:staticcheck // SA1012: nil context is exactly what we test
		t.Fatal("nil ctx")
	}
}

func TestRedaction(t *testing.T) {
	s, m := newSys(t, Options{HashKey: []byte("k")})
	log := s.For(CatAuth, "auth")
	log.Info("login",
		"password", "hunter2", "smtpPass", "x", "Authorization", "Bearer y", "api_key", "z",
		"refresh_token", "r", "webhookSecret", "w", "code", "123456", "dsn", "https://k@sentry/1",
		"pw", Secret("s3cret"),
		Address("to", "Mario@Example.com"),
		"uri", "mongodb://eb:pa55@mongo:27017/?replicaSet=rs0",
		"cb", "https://x.test/cb?token=abc&x=1",
		"body", make([]byte, 300),
		"plain", "no url here",
		slog.Group("nested", slog.String("clientSecret", "c"), slog.Int("n", 1)),
		"m", map[string]any{"token": "t", "ok": "v"},
		"list", []string{"https://a:b@h/x"},
	)
	flush(t, s)
	a := m.all()[0].Attrs
	for _, k := range []string{"password", "smtpPass", "Authorization", "api_key", "refresh_token", "webhookSecret", "code", "dsn", "pw"} {
		if a[k] != redacted {
			t.Errorf("%s = %v, want redacted", k, a[k])
		}
	}
	if a["to"] != "Mario@Example.com" {
		t.Errorf("address without hashing = %v", a["to"])
	}
	if s := a["uri"].(string); strings.Contains(s, "pa55") || !strings.Contains(s, "redacted") {
		t.Errorf("uri = %s", s)
	}
	if s := a["cb"].(string); strings.Contains(s, "abc") || !strings.Contains(s, "x=1") {
		t.Errorf("cb = %s", s)
	}
	if a["body"] != "{300 bytes}" || a["plain"] != "no url here" {
		t.Errorf("body/plain = %v %v", a["body"], a["plain"])
	}
	nested := a["nested"].(map[string]any)
	if nested["clientSecret"] != redacted || nested["n"] != int64(1) {
		t.Errorf("nested = %v", nested)
	}
	if mm := a["m"].(map[string]any); mm["token"] != redacted || mm["ok"] != "v" {
		t.Errorf("map = %v", mm)
	}
	if l := a["list"].([]any); strings.Contains(l[0].(string), ":b@") {
		t.Errorf("list = %v", l)
	}
	if Secret("x").String() != redacted {
		t.Error("Secret.String leaks")
	}
	// hashing on
	st := s.Settings()
	st.HashAddresses = true
	s.Apply(st)
	s.For(CatSend, "send").Info("sent", Address("to", "Mario@Example.com"), Address("cc", "mario@example.com "), "code", "250")
	flush(t, s)
	r := m.all()[1]
	if h := r.Attrs["to"].(string); !strings.HasPrefix(h, "h:") || len(h) != 14 || h != r.Attrs["cc"] {
		t.Errorf("hash = %v / %v", r.Attrs["to"], r.Attrs["cc"])
	}
	if r.Code != "250" {
		t.Errorf("code outside auth must be the code field: %+v", r)
	}
	// protocol keeps raw bytes
	s.For(CatProtocol, "imap").Debug("line", "raw", []byte("A1 OK"))
	flush(t, s)
	if m.all()[2].Attrs["raw"] != "A1 OK" {
		t.Errorf("protocol bytes = %v", m.all()[2].Attrs["raw"])
	}
	if Size("body", 5).Value.String() != "{5 bytes}" {
		t.Error("Size")
	}
}

func TestLevelsSettingsAndSampling(t *testing.T) {
	s, m := newSys(t, Options{})
	log := s.For(CatSync, "x")
	log.Debug("dropped by level")
	log.Info("kept")
	if !log.Enabled(context.Background(), LevelInfo) || log.Enabled(context.Background(), LevelDebug) {
		t.Fatal("Enabled wrong")
	}
	st := s.Settings().WithDefaultLevel(LevelWarn)
	cs := st.Categories[CatSync]
	cs.Sampling = map[slog.Level]float64{LevelWarn: 0, LevelInfo: 0}
	st.Categories[CatSync] = cs
	api := st.Categories[CatAPI]
	api.Enabled = false
	st.Categories[CatAPI] = api
	s.Apply(st)
	log.Info("now below level")
	log.Warn("warn is never sampled away")
	s.For(CatAPI, "api").Error("disabled category")
	if st.Categories[CatProtocol].Level != LevelDebug {
		t.Fatal("protocol keeps its default level")
	}
	flush(t, s)
	var msgs []string
	for _, r := range m.all() {
		msgs = append(msgs, r.Msg)
	}
	if strings.Join(msgs, "|") != "kept|warn is never sampled away" {
		t.Fatalf("msgs = %v", msgs)
	}
	ret := DefaultSettings().WithRetention(3 * 24 * time.Hour)
	if ret.Categories[CatSync].Retention != 3*24*time.Hour || ret.Categories[CatAudit].Retention != 365*day {
		t.Fatal("retention override")
	}
	if DefaultSettings().WithRetention(0).Categories[CatSync].Retention != 14*day {
		t.Fatal("zero retention must keep defaults")
	}
	// unknown category → system + uncategorised counter
	s.For("bogus", "x").Warn("u")
	s.Default().Warn("plain slog")
	flush(t, s)
	if got := s.Stats().Uncategorised; got != 2 {
		t.Fatalf("uncategorised = %d", got)
	}
}

func TestBackPressureNeverBlocksAndCountsDrops(t *testing.T) {
	s, m := newSys(t, Options{BufferSize: 20, BatchSize: 1, FlushEvery: time.Hour})
	m.mu.Lock()
	m.block = make(chan struct{})
	m.mu.Unlock()
	log := s.For(CatSync, "bp")
	st := s.Settings()
	cs := st.Categories[CatSync]
	cs.Level = LevelTrace
	st.Categories[CatSync] = cs
	s.Apply(st)
	// first record is taken by the writer and blocks in the sink; then fill the queue
	log.Info("first")
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	for i := 0; i < 16; i++ { // 80 % of 20
		log.Info("fill")
	}
	log.Log(context.Background(), LevelTrace, "trace at 80%") // dropped
	log.Debug("debug at 80%")                                 // dropped
	log.Info("info at 80%")                                   // kept (17)
	log.Info("info")                                          // kept (18)
	log.Info("info")                                          // kept (19) → 95 %
	log.Info("info at 95%")                                   // dropped
	log.Warn("warn fills last slot")                          // kept (20)
	log.Error("error when full")                              // dropped, counted
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("logging blocked the caller")
	}
	st2 := s.Stats()
	if st2.Dropped[CatSync]["trace"] != 1 || st2.Dropped[CatSync]["debug"] != 1 || st2.Dropped[CatSync]["info"] != 1 || st2.Dropped[CatSync]["error"] != 1 {
		t.Fatalf("dropped = %v", st2.Dropped)
	}
	m.mu.Lock()
	close(m.block)
	m.block = nil
	m.mu.Unlock()
	flush(t, s)
	var report *Record
	for _, r := range m.all() {
		if r.Msg == "log records dropped" {
			rr := r
			report = &rr
		}
	}
	if report == nil || report.Attrs["dropped"] != uint64(4) || report.LevelName != "warn" || report.Category != CatSystem {
		t.Fatalf("drop report = %+v", report)
	}
	if s.Stats().BufferCap != 20 {
		t.Fatal("buffer cap")
	}
}

func TestFatalFlushesSynchronously(t *testing.T) {
	s, m := newSys(t, Options{FlushEvery: time.Hour, BatchSize: 1000})
	s.For(CatCrash, "panic").Log(context.Background(), LevelFatal, "unrecoverable", "stack", "goroutine 1")
	recs := m.all()
	if len(recs) != 1 || recs[0].LevelName != "fatal" || recs[0].Stack != "goroutine 1" {
		t.Fatalf("fatal not flushed: %+v", recs)
	}
	s.For(CatCrash, "panic").Error("recovered panic")
	flush(t, s)
	if !strings.Contains(m.all()[1].Stack, "goroutine") {
		t.Fatal("crash error must capture a stack")
	}
}

func TestStdoutMirrorFormats(t *testing.T) {
	var out bytes.Buffer
	s, _ := newSys(t, Options{Stdout: &out})
	s.For(CatSystem, "boot").Info("hidden while off")
	flush(t, s)
	if out.Len() != 0 {
		t.Fatal("stdout must be off by default")
	}
	st := s.Settings()
	st.Stdout = "json"
	s.Apply(st)
	ctx := With(context.Background(), func(id *IDs) { id.RequestID = "req_9"; id.OrganizationID = "org_1" })
	s.For(CatAPI, "http").InfoContext(ctx, "request", "status", 200, "code", "ok")
	flush(t, s)
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &m); err != nil {
		t.Fatalf("json line: %v %q", err, out.String())
	}
	if m["category"] != "api" || m["requestId"] != "req_9" || m["organizationId"] != "org_1" || m["code"] != "ok" {
		t.Fatalf("json = %v", m)
	}
	out.Reset()
	st.Stdout = "text"
	s.Apply(st)
	s.For(CatAPI, "http").InfoContext(ctx, "request", "status", 200)
	flush(t, s)
	if line := out.String(); !strings.Contains(line, "[api] http request status=200") || !strings.Contains(line, "requestId=req_9") {
		t.Fatalf("text = %q", line)
	}
	st.Stdout = "weird"
	s.Apply(st)
	out.Reset()
	s.For(CatAPI, "http").Info("x")
	flush(t, s)
	if out.Len() != 0 {
		t.Fatal("unknown format must mean off")
	}
}

func TestSinkErrorsAndLifecycle(t *testing.T) {
	var fb bytes.Buffer
	s := New(Options{Stdout: &bytes.Buffer{}, Fallback: &fb})
	bad := &memSink{failErr: errors.New("disk full")}
	s.AddSink(bad)
	s.For(CatSystem, "x").Info("a")
	s.For(CatSystem, "x").Info("b")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Stats().SinkErrors == 0 || !strings.Contains(fb.String(), "disk full") {
		t.Fatalf("sink error not reported: %d %q", s.Stats().SinkErrors, fb.String())
	}
	s.RemoveSink("mem")
	if len(*s.sinks.Load()) != 0 {
		t.Fatal("RemoveSink")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal("second close")
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal("flush after close")
	}
	s.For(CatSystem, "x").Info("after close")
	if s.Stats().Dropped[CatSystem]["info"] != 1 {
		t.Fatal("records after close must be counted as dropped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s2 := New(Options{Stdout: &bytes.Buffer{}})
	if err := s2.Flush(ctx); err == nil {
		t.Log("flush with cancelled ctx may succeed if the writer is idle")
	}
	_ = s2.Close(context.Background())
}
