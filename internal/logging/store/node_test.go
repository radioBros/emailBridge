package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

func TestLimiter(t *testing.T) {
	l := &Limiter{Rate: 3}
	now := base
	admitted := 0
	for i := 0; i < 10; i++ {
		if l.Admit(now) {
			admitted++
		}
	}
	l.Drop(2)
	if admitted != 3 || l.TakeDropped() != 9 || l.TakeDropped() != 0 {
		t.Fatal("window cap and drop count")
	}
	if !l.Admit(now.Add(time.Second)) {
		t.Fatal("new window admits")
	}
	s := SummaryRecord("n1", 9, now)
	if s.Msg != "9 more" || s.Attrs["more"] != int64(9) || !strings.HasPrefix(s.ID, "log_") || len(s.ID) != 30 {
		t.Fatalf("summary %+v", s)
	}
}

func TestMatches(t *testing.T) {
	r := FromRecord(&logging.Record{ID: "log_x", Time: base, Level: logging.LevelWarn, Category: logging.CatSync, Msg: "IDLE dropped",
		IDs: logging.IDs{OrganizationID: "org_a", AccountID: "acc_1"}, Attrs: map[string]any{"host": "imap.example.com"}})
	for _, c := range []struct {
		f    Filter
		want bool
	}{
		{Filter{}, true},
		{Filter{Categories: []logging.Category{logging.CatSync}}, true},
		{Filter{Categories: []logging.Category{logging.CatAPI}}, false},
		{Filter{Level: "warn"}, true},
		{Filter{Level: "error"}, false},
		{Filter{Level: "bogus"}, false},
		{Filter{OrgID: "org_a", AccountID: "acc_1"}, true},
		{Filter{OrgID: "org_b"}, false},
		{Filter{RequestID: "req"}, false},
		{Filter{Q: "idle"}, true},
		{Filter{Q: "EXAMPLE.com"}, true},
		{Filter{Q: "nothing"}, false},
	} {
		if got := c.f.Matches(&r); got != c.want {
			t.Errorf("%+v: %v", c.f, got)
		}
	}
	noAttrs := r
	noAttrs.Attrs = nil
	if (&Filter{Q: "example"}).Matches(&noAttrs) {
		t.Error("q on record without attrs")
	}
	bad := r
	bad.Level = "loud"
	if (&Filter{Level: "info"}).Matches(&bad) {
		t.Error("unparseable record level")
	}
}

func TestHubDeliversWithRateCapAndSummary(t *testing.T) {
	h := NewHub("n1", 5)
	defer h.Close()
	var mu sync.Mutex
	var got []LogRecord
	cancel := h.Subscribe(Filter{Categories: []logging.Category{logging.CatAPI}}, func(r LogRecord) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})
	if h.Name() != TailSinkName || h.Subscribers() != 1 {
		t.Fatal("hub identity")
	}
	var batch []logging.Record
	for i := 0; i < 20; i++ {
		batch = append(batch, rec(time.Now(), i, logging.CatAPI, logging.LevelInfo, "request", nil))
	}
	batch = append(batch, rec(time.Now(), 99, logging.CatSync, logging.LevelInfo, "not matching", nil))
	if err := h.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d records", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	last := got[5]
	mu.Unlock()
	if last.Msg != "15 more" || last.Component != "logging.tail" {
		t.Fatalf("summary %+v", last)
	}
	cancel()
	cancel()
	if h.Subscribers() != 0 {
		t.Fatal("cancel")
	}
	if err := h.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}

func TestHubFullBufferCountsDrops(t *testing.T) {
	h := NewHub("n1", 10000)
	defer h.Close()
	block := make(chan struct{})
	var mu sync.Mutex
	var summaries int64
	cancel := h.Subscribe(Filter{}, func(r LogRecord) {
		<-block
		if r.Component == "logging.tail" {
			mu.Lock()
			summaries += r.Attrs["more"].(int64)
			mu.Unlock()
		}
	})
	defer cancel()
	var batch []logging.Record
	for i := 0; i < 400; i++ {
		batch = append(batch, rec(time.Now(), i, logging.CatAPI, logging.LevelInfo, "flood", nil))
	}
	_ = h.Write(context.Background(), batch)
	close(block)
	deadline := time.Now().Add(4 * time.Second)
	for {
		mu.Lock()
		n := summaries
		mu.Unlock()
		if n >= 400-257 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dropped records not summarised: %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type pubRec struct {
	mu   sync.Mutex
	msgs map[string][]string
}

func (p *pubRec) PublishCore(subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.msgs == nil {
		p.msgs = map[string][]string{}
	}
	p.msgs[subject] = append(p.msgs[subject], string(data))
	return nil
}

func (p *pubRec) count(subject string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.msgs[subject])
}

func call(t *testing.T, n *Node, req Request) Reply {
	t.Helper()
	b, _ := json.Marshal(req)
	var rep Reply
	if err := json.Unmarshal(n.Handle(context.Background(), b), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Node != "n1" {
		t.Fatalf("reply node %q", rep.Node)
	}
	return rep
}

func TestNodeHandleOps(t *testing.T) {
	s := openTest(t, nil)
	seed(t, s)
	h := NewHub("n1", 0)
	defer h.Close()
	pub := &pubRec{}
	now := base
	n := &Node{Store: s, Hub: h, Pub: pub, Now: func() time.Time { return now }}
	f := rangeFilter()
	f.Limit = 10
	rep := call(t, n, Request{Op: OpQuery, Filter: &f, TimeoutMs: 5000})
	if len(rep.Records) != 10 || rep.Truncated || rep.Error != "" {
		t.Fatalf("query reply %+v", rep)
	}
	var first LogRecord
	_ = json.Unmarshal(rep.Records[0], &first)
	if first.Time != base.UnixNano() {
		t.Fatalf("first %+v", first)
	}
	if rep := call(t, n, Request{Op: OpGet, ID: first.ID}); rep.Record == nil || rep.Record.ID != first.ID {
		t.Fatalf("get %+v", rep)
	}
	if rep := call(t, n, Request{Op: OpGet, ID: "nope"}); rep.Error != ReplyBadFilter {
		t.Fatalf("bad get %+v", rep)
	}
	if rep := call(t, n, Request{Op: OpStats, Filter: &f}); rep.Stats == nil || rep.Stats.ByCategory["api"] != 40 {
		t.Fatalf("stats %+v", rep)
	}
	if rep := call(t, n, Request{Op: OpErase, AccountID: "acc_1"}); rep.Erased != 24 {
		t.Fatalf("erase %+v", rep)
	}
	bad := f
	bad.Limit = 0
	if rep := call(t, n, Request{Op: OpQuery, Filter: &bad}); rep.Error != ReplyBadFilter {
		t.Fatalf("bad filter %+v", rep)
	}
	for _, req := range []Request{{Op: OpQuery}, {Op: OpStats}, {Op: "drop"}, {Op: OpTail, TailID: "x", Filter: &f}} {
		if rep := call(t, n, req); rep.Error != ReplyBadRequest {
			t.Errorf("%+v: %+v", req, rep)
		}
	}
	var rep2 Reply
	_ = json.Unmarshal(n.Handle(context.Background(), []byte("{")), &rep2)
	if rep2.Error != ReplyBadRequest {
		t.Fatal("garbage request")
	}
	// remote tail: start, refresh, records published, expiry
	tf := Filter{Categories: []logging.Category{logging.CatAudit}}
	call(t, n, Request{Op: OpTail, TailID: "tail_abcdef12", Filter: &tf})
	call(t, n, Request{Op: OpTail, TailID: "tail_abcdef12", Filter: &tf})
	if n.Tails() != 1 {
		t.Fatalf("tails %d", n.Tails())
	}
	_ = h.Write(context.Background(), []logging.Record{rec(time.Now(), 1, logging.CatAudit, logging.LevelInfo, "audited", nil)})
	deadline := time.Now().Add(2 * time.Second)
	for pub.count(TailSubject("tail_abcdef12")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("remote tail did not publish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	now = now.Add(RemoteTailTTL + time.Second)
	n.ExpireTails()
	if n.Tails() != 0 || h.Subscribers() != 0 {
		t.Fatal("expired tail must stop")
	}
	call(t, n, Request{Op: OpTail, TailID: "tail_abcdef12", Filter: &tf})
	call(t, n, Request{Op: OpUntail, TailID: "tail_abcdef12"})
	if n.Tails() != 0 {
		t.Fatal("untail")
	}
	call(t, n, Request{Op: OpTail, TailID: "tail_abcdef99", Filter: &tf})
	n.stopTails()
	if h.Subscribers() != 0 {
		t.Fatal("stopTails")
	}
	// a broken store reports a generic failure
	_ = s.Close()
	if rep := call(t, n, Request{Op: OpQuery, Filter: &f}); rep.Error != ReplyFailed {
		t.Fatalf("closed store %+v", rep)
	}
}

func TestNodeFitsRepliesIntoPayload(t *testing.T) {
	s := openTest(t, nil)
	big := strings.Repeat("x", 3000)
	var recs []logging.Record
	for i := 0; i < 40; i++ {
		recs = append(recs, rec(base.Add(-time.Duration(i)*time.Second), i, logging.CatCrash, logging.LevelError, "panic", func(r *logging.Record) {
			r.Stack = big
		}))
	}
	write(t, s, recs...)
	n := &Node{Store: s, MaxPayload: 32 << 10}
	f := rangeFilter()
	b, _ := json.Marshal(Request{Op: OpQuery, Filter: &f})
	out := n.Handle(context.Background(), b)
	var rep Reply
	_ = json.Unmarshal(out, &rep)
	if !rep.Truncated || len(rep.Records) == 0 || len(rep.Records) >= 40 || int64(len(out)) > n.MaxPayload {
		t.Fatalf("truncation: %d records, %d bytes, truncated=%v", len(rep.Records), len(out), rep.Truncated)
	}
	// one record larger than the payload is slimmed so paging progresses
	n.MaxPayload = 4<<10 + 1500
	out = n.Handle(context.Background(), b)
	rep = Reply{}
	_ = json.Unmarshal(out, &rep)
	var r LogRecord
	if len(rep.Records) != 1 || json.Unmarshal(rep.Records[0], &r) != nil || r.Stack != nil || r.Attrs["_truncated"] != true {
		t.Fatalf("slim record: %+v", rep)
	}
}
