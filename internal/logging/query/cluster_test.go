package query

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

type cluster struct {
	b      *bus.Bus
	s1, s2 *store.Store
	h1, h2 *store.Hub
	n1     *store.Node
}

// newCluster runs two log-store nodes (n1, n2) answering eb.logs.query on one embedded NATS server.
func newCluster(t *testing.T, mut func(n1 *store.Node)) *cluster {
	t.Helper()
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	c := &cluster{b: b, s1: openStore(t, "n1"), s2: openStore(t, "n2"), h1: store.NewHub("n1", 0), h2: store.NewHub("n2", 0)}
	t.Cleanup(c.h1.Close)
	t.Cleanup(c.h2.Close)
	c.n1 = &store.Node{Store: c.s1, Hub: c.h1, Pub: b}
	n2 := &store.Node{Store: c.s2, Hub: c.h2, Pub: b}
	if mut != nil {
		mut(c.n1)
	}
	for _, n := range []*store.Node{c.n1, n2} {
		stop, err := n.Serve(b)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stop)
	}
	return c
}

func (c *cluster) service(t *testing.T, nodes []string, deadline time.Duration) *Service {
	t.Helper()
	svc, err := New(Options{Node: "n1", Bus: c.b, Local: c.n1, Deadline: deadline, TailRate: 3,
		Nodes: func(context.Context) ([]string, error) { return nodes, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestClusterMergeWithTimedOutNode(t *testing.T) {
	c := newCluster(t, nil)
	seedStore(t, c.s1, 100, 0, time.Minute)
	seedStore(t, c.s2, 100, 30*int(time.Second), time.Minute) // interleaved 30 s later
	// n3 is registered but slow: it answers after the deadline
	unsub, err := c.b.Respond(bus.LogsQuerySubject, func(ctx context.Context, _ []byte) []byte {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		b, _ := json.Marshal(store.Reply{Node: "n3"})
		return b
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	ctx := context.Background()
	from, to := dayRange()
	svc := c.service(t, []string{"n1", "n2", "n3"}, 400*time.Millisecond)
	start := time.Now()
	p, err := svc.Search(ctx, Query{From: from, To: to, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Partial, []string{"n3"}) || len(p.Records) != 10 || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("partial %v, %d records, %v", p.Partial, len(p.Records), time.Since(start))
	}
	nodes := map[string]int{}
	for i, r := range p.Records {
		nodes[r.Node]++
		if i > 0 && !r.Pos().Before(p.Records[i-1].Pos()) {
			t.Fatal("merged order")
		}
	}
	if nodes["n1"] != 5 || nodes["n2"] != 5 {
		t.Fatalf("interleaving %v", nodes)
	}

	// all answering: returns as soon as both replied, pages through 200 records without gaps
	svc = c.service(t, []string{"n1", "n2"}, 5*time.Second)
	seen := map[string]bool{}
	q := Query{From: from, To: to, Limit: 33}
	for {
		start := time.Now()
		p, err := svc.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Partial) != 0 || time.Since(start) > time.Second {
			t.Fatalf("no partial expected: %v after %v", p.Partial, time.Since(start))
		}
		for _, r := range p.Records {
			if seen[r.ID] {
				t.Fatal("duplicate")
			}
			seen[r.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	if len(seen) != 200 {
		t.Fatalf("cluster paging saw %d", len(seen))
	}

	// get finds a record on the other node; stats and erase sum over nodes
	id := newID(base.Add(-5*time.Minute-30*time.Second), 30*int(time.Second)+5)
	r, _, err := svc.Get(ctx, id)
	if err != nil || r == nil || r.ID != id {
		t.Fatalf("cluster get %+v %v", r, err)
	}
	st, err := svc.Stats(ctx, from, to, "")
	if err != nil || st.ByCategory["api"] != 100 || len(st.Disk["api"]) != 2 {
		t.Fatalf("cluster stats %+v %v", st, err)
	}
	n, partial, err := svc.Erase(ctx, "acc_0")
	if err != nil || n != 50 || len(partial) != 0 {
		t.Fatalf("cluster erase %d %v %v", n, partial, err)
	}
	// a filter every node rejects is a validation error
	if _, err := svc.Search(ctx, Query{From: from, To: to, MinLevel: "nope"}); codeOf(t, err) != errs.CodeValidationFailed {
		t.Fatal("validation")
	}
	// the default expected set (empty registry) is this node only
	def, _ := New(Options{Node: "n1", Bus: c.b})
	if got, known := def.expected(ctx); !slices.Equal(got, []string{"n1"}) || !known {
		t.Fatalf("default expected %v %v", got, known)
	}
	// a registered node joins the expected set
	if err := c.b.RegisterNode(ctx, bus.NodeInfo{NodeID: "n2"}); err != nil {
		t.Fatal(err)
	}
	if got, known := def.expected(ctx); !slices.Equal(got, []string{"n1", "n2"}) || !known {
		t.Fatalf("registry expected %v %v", got, known)
	}
}

func TestClusterPagingWithTruncatedReplies(t *testing.T) {
	c := newCluster(t, func(n1 *store.Node) { n1.MaxPayload = 16 << 10 })
	big := strings.Repeat("s", 1500)
	var recs []logging.Record
	for i := 0; i < 80; i++ {
		r := mkRec(base.Add(-time.Duration(i)*time.Minute), i, logging.CatCrash, logging.LevelError, "panic recovered", "")
		r.Stack = big
		recs = append(recs, r)
	}
	if err := c.s1.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	seedStore(t, c.s2, 80, 1000, time.Minute)
	svc := c.service(t, []string{"n1", "n2"}, 5*time.Second)
	from, to := dayRange()
	q := Query{From: from, To: to, Limit: 100}
	seen := map[string]bool{}
	var prev *store.Position
	pages := 0
	for {
		p, err := svc.Search(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range p.Records {
			if seen[r.ID] || (prev != nil && !r.Pos().Before(*prev)) {
				t.Fatal("duplicate or out of order")
			}
			seen[r.ID] = true
			pos := r.Pos()
			prev = &pos
		}
		pages++
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	if len(seen) != 160 || pages < 3 {
		t.Fatalf("saw %d records in %d pages", len(seen), pages)
	}
}

func TestClusterTail(t *testing.T) {
	c := newCluster(t, nil)
	svc := c.service(t, []string{"n1", "n2"}, 2*time.Second)
	var mu sync.Mutex
	var got []store.LogRecord
	stop, err := svc.Tail(context.Background(), TailFilter{Categories: []logging.Category{logging.CatAPI}}, func(r store.LogRecord) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.h1.Subscribers() != 1 || c.h2.Subscribers() != 1 {
		t.Fatalf("remote tails not registered: %d %d", c.h1.Subscribers(), c.h2.Subscribers())
	}
	var batch []logging.Record
	for i := 0; i < 10; i++ {
		batch = append(batch, mkRec(time.Now(), i, logging.CatAPI, logging.LevelInfo, "from n2", orgA))
	}
	_ = c.h2.Write(context.Background(), batch)
	_ = c.h1.Write(context.Background(), []logging.Record{mkRec(time.Now(), 99, logging.CatSync, logging.LevelInfo, "filtered out", orgA)})
	// exactly TailRate (3) records pass; the other 7 arrive as "N more" summaries (possibly split over
	// several one-second ticks)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		normal, more := 0, int64(0)
		for _, r := range got {
			if isSummary(&r) {
				more += r.Attrs["more"].(int64)
				continue
			}
			if r.Msg != "from n2" {
				t.Errorf("unexpected record %q", r.Msg)
			}
			normal++
		}
		mu.Unlock()
		if normal == 3 && more == 7 {
			break
		}
		if normal > 3 || more > 7 || time.Now().After(deadline) {
			t.Fatalf("tail delivered %d records and %d more", normal, more)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	stop()
	if c.h1.Subscribers() != 0 || c.h2.Subscribers() != 0 {
		t.Fatal("untail must stop remote tails")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestUnknownMembershipWaitsForEveryAnswer(t *testing.T) {
	ctx := context.Background()
	addr := freeAddr(t)
	srv, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "srv", DataDir: t.TempDir(), Listen: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ext, err := bus.Open(ctx, bus.Options{Mode: bus.ModeExternal, NodeID: "n1", URL: "nats://" + addr})
	if err != nil {
		t.Fatal(err)
	}
	defer ext.Close()
	for _, name := range []string{"n1", "n2"} {
		s := openStore(t, name)
		seedStore(t, s, 5, len(name)*1000+int(name[1]), time.Minute)
		stop, err := (&store.Node{Store: s}).Serve(srv)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
	}
	svc, err := New(Options{Node: "n1", Bus: ext, Deadline: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, known := svc.expected(ctx); known {
		t.Fatal("external bus with an empty registry has unknown membership")
	}
	from, to := dayRange()
	start := time.Now()
	p, err := svc.Search(ctx, Query{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Records) != 10 || len(p.Partial) != 0 || time.Since(start) < 400*time.Millisecond {
		t.Fatalf("%d records, partial %v, after %v", len(p.Records), p.Partial, time.Since(start))
	}
}
