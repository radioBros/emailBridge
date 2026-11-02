package bus

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// nodeLeases gives each simulated node its own view of the registry on top of one real bus.
type nodeLeases struct {
	*Bus
	nodes func() []string
	err   error
}

func (n nodeLeases) NodeIDs(context.Context) ([]string, error) {
	if n.err != nil {
		return nil, n.err
	}
	return n.nodes(), nil
}

type tracker struct {
	mu      sync.Mutex
	running map[int]string
	overlap atomic.Int32
}

func (tr *tracker) work(node string) func(ctx context.Context, p int, _ uint64) {
	return func(ctx context.Context, p int, _ uint64) {
		tr.mu.Lock()
		if other, ok := tr.running[p]; ok && other != node {
			tr.overlap.Add(1)
		}
		tr.running[p] = node
		tr.mu.Unlock()
		<-ctx.Done()
		tr.mu.Lock()
		if tr.running[p] == node {
			delete(tr.running, p)
		}
		tr.mu.Unlock()
	}
}

func (tr *tracker) snapshot() map[int]string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make(map[int]string, len(tr.running))
	for k, v := range tr.running {
		out[k] = v
	}
	return out
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBalancerSpreadsAndRebalances(t *testing.T) {
	b := openEmbedded(t)
	const n = 16
	var mu sync.Mutex
	live := []string{"a", "b", "c"}
	view := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), live...) }
	tr := &tracker{running: map[int]string{}}
	cancels := map[string]context.CancelFunc{}
	dones := map[string]chan struct{}{}
	bals := map[string]*Balancer{}
	for _, id := range []string{"a", "b", "c"} {
		ctx, cancel := context.WithCancel(context.Background())
		bal := &Balancer{Leases: nodeLeases{Bus: b, nodes: view}, Prefix: "test", N: n, NodeID: id, TTL: 2 * time.Second, Work: tr.work(id)}
		done := make(chan struct{})
		go func() { defer close(done); bal.Run(ctx) }()
		cancels[id], dones[id], bals[id] = cancel, done, bal
	}
	t.Cleanup(func() {
		for id, c := range cancels {
			c()
			<-dones[id]
		}
	})
	waitFor(t, 8*time.Second, func() bool { return len(tr.snapshot()) == n }, "not every partition is worked")
	for p, node := range tr.snapshot() {
		if want := Owner("test", p, []string{"a", "b", "c"}); node != want {
			t.Fatalf("partition %d on %s, preferred %s", p, node, want)
		}
	}
	// node c leaves: its partitions move to a and b, nothing runs twice
	mu.Lock()
	live = []string{"a", "b"}
	mu.Unlock()
	cancels["c"]()
	<-dones["c"]
	delete(cancels, "c")
	waitFor(t, 5*time.Second, func() bool {
		s := tr.snapshot()
		if len(s) != n {
			return false
		}
		for _, node := range s {
			if node == "c" {
				return false
			}
		}
		return true
	}, "partitions of the stopped node did not move")
	if tr.overlap.Load() != 0 {
		t.Fatalf("%d partitions ran on two nodes at once", tr.overlap.Load())
	}
	held := len(bals["a"].Held()) + len(bals["b"].Held())
	if held != n {
		t.Fatalf("held %d", held)
	}
}

func TestBalancerTakesOverExpiredLease(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	// a crashed node "x" holds partition 0 without renewing
	if _, err := b.Claim(ctx, "crash.0", []byte("x"), time.Second); err != nil {
		t.Fatal(err)
	}
	tr := &tracker{running: map[int]string{}}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	bal := &Balancer{Leases: nodeLeases{Bus: b, nodes: func() []string { return nil }}, Prefix: "crash", N: 1, NodeID: "y", TTL: time.Second, Work: tr.work("y")}
	go bal.Run(rctx)
	waitFor(t, 5*time.Second, func() bool { return tr.snapshot()[0] == "y" }, "expired lease not taken over")
}

func TestBalancerKeepsLastViewWhenRegistryFails(t *testing.T) {
	b := openEmbedded(t)
	tr := &tracker{running: map[int]string{}}
	nl := nodeLeases{Bus: b, nodes: func() []string { return []string{"solo"} }}
	bal := &Balancer{Leases: nl, Prefix: "view", N: 4, NodeID: "solo", TTL: time.Second, Work: tr.work("solo")}
	ctx := context.Background()
	bal.tick(ctx)
	if len(bal.Held()) != 4 {
		t.Fatalf("held %v", bal.Held())
	}
	bal.Leases = nodeLeases{Bus: b, err: errors.New("registry down")}
	bal.tick(ctx)
	if len(bal.Held()) != 4 {
		t.Fatal("a registry outage must not drop partitions")
	}
	bal.releaseAll(ctx)
	if len(bal.Held()) != 0 {
		t.Fatal("release all")
	}
	for p := 0; p < 4; p++ {
		if h, _ := b.LeaseHolder(ctx, fmt.Sprintf("view.%d", p)); h != nil {
			t.Fatalf("lease view.%d still held after shutdown", p)
		}
	}
}

func TestOwnerIsStableAndSpread(t *testing.T) {
	nodes := []string{"n1", "n2", "n3", "n4"}
	count := map[string]int{}
	for p := 0; p < 256; p++ {
		o := Owner("evt", p, nodes)
		if o != Owner("evt", p, []string{"n4", "n3", "n2", "n1"}) {
			t.Fatal("owner depends on node order")
		}
		count[o]++
	}
	for _, n := range nodes {
		if count[n] < 40 { // 64 expected each
			t.Fatalf("uneven spread %v", count)
		}
	}
	if Owner("evt", 1, nil) != "" {
		t.Fatal("no nodes")
	}
}

func TestNodeRegistry(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: "n1", Roles: []string{"api"}, Version: "dev"}); err != nil {
		t.Fatal(err)
	}
	nodes, err := b.LiveNodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].NodeID != "n1" || nodes[0].SeenAt.IsZero() {
		t.Fatalf("%v %+v", err, nodes)
	}
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); b.heartbeat(hctx, NodeInfo{NodeID: "n2"}, 50*time.Millisecond, b.o.Log) }()
	waitFor(t, 2*time.Second, func() bool { ids, _ := b.NodeIDs(ctx); return len(ids) == 2 }, "heartbeat did not register")
	cancel()
	<-done
	ids, _ := b.NodeIDs(ctx)
	if len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("deregister on stop: %v", ids)
	}
	if err := b.DeregisterNode(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	var nb *Bus
	if nb.RegisterNode(ctx, NodeInfo{}) == nil || nb.DeregisterNode(ctx, "x") == nil {
		t.Fatal("nil bus")
	}
}

func TestSubscribeStream(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if _, err := b.Publish(ctx, EventSubject(0, "org_x", "message.received"), []byte("one"), "id-1", nil); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	stop, err := b.SubscribeStream(ctx, StreamEvents, "eb.evt.>", true, func(_ string, data []byte) { got <- string(data) })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case v := <-got:
		if v != "one" {
			t.Fatal(v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no message")
	}
	var nb *Bus
	if _, err := nb.SubscribeStream(ctx, StreamEvents, "", false, nil); err == nil {
		t.Fatal("nil bus")
	}
}

func TestBalancerRoleLimitsPlacement(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	for _, n := range []NodeInfo{
		{NodeID: "wh-1", Roles: []string{"webhook"}},
		{NodeID: "sync-1", Roles: []string{"sync", "submit"}},
		{NodeID: "wh-2", Roles: []string{"api", "webhook"}, Draining: true},
	} {
		if err := b.RegisterNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	role := &Balancer{Leases: b, Prefix: "evt", Roles: []string{"webhook"}, N: 4, NodeID: "wh-1"}
	got, err := role.candidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"wh-1"}) { // sync-1 lacks the role, wh-2 drains
		t.Fatalf("webhook candidates %v", got)
	}
	relay := &Balancer{Leases: b, Prefix: "relay", Roles: []string{"sync", "submit", "api"}, N: 4, NodeID: "wh-1"}
	if ids, _ := relay.candidates(ctx); len(ids) != 1 || ids[0] != "sync-1" { // any of the roles; wh-2 drains
		t.Fatalf("relay candidates %v", ids)
	}
	all := &Balancer{Leases: b, Prefix: "relay", N: 4, NodeID: "wh-1"}
	if ids, _ := all.candidates(ctx); len(ids) < 3 {
		t.Fatalf("without a role every live node is a candidate: %v", ids)
	}
	// Placement never lands on the sync-only node.
	for p := range 64 {
		if Owner("evt", p, got) == "sync-1" {
			t.Fatal("partition placed on a node without the role")
		}
	}
}
