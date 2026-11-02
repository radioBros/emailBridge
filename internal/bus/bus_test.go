package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func openEmbedded(t *testing.T) *Bus {
	t.Helper()
	b, err := Open(context.Background(), Options{Mode: ModeEmbedded, NodeID: "t1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestOpenDeclaresStreamsAndBucketsIdempotently(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	b, err := Open(ctx, Options{Mode: ModeEmbedded, NodeID: "n1", DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{StreamEvents, StreamDelivery, StreamSubmit, StreamSysmail} {
		s, err := b.JS.Stream(ctx, name)
		if err != nil {
			t.Fatalf("stream %s: %v", name, err)
		}
		info, _ := s.Info(ctx)
		if info.Config.Duplicates != DedupWindow {
			t.Errorf("%s dedup window %v", name, info.Config.Duplicates)
		}
	}
	ev, _ := b.JS.Stream(ctx, StreamEvents)
	evInfo, _ := ev.Info(ctx)
	if evInfo.Config.Retention != jetstream.LimitsPolicy || evInfo.Config.MaxAge != 72*time.Hour {
		t.Errorf("events config %+v", evInfo.Config)
	}
	dl, _ := b.JS.Stream(ctx, StreamDelivery)
	dlInfo, _ := dl.Info(ctx)
	if dlInfo.Config.Retention != jetstream.WorkQueuePolicy {
		t.Error("delivery must be a work queue")
	}
	if b.NodesBucket() == nil || b.Cache() == nil {
		t.Fatal("buckets")
	}
	b.Close()
	// reopen on the same store: declarations are idempotent
	b2, err := Open(ctx, Options{Mode: ModeEmbedded, NodeID: "n1", DataDir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	b2.Close()
	if err := (&Bus{}).Ready(ctx); err == nil {
		t.Fatal("zero bus must not be ready")
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, Options{Mode: ModeEmbedded}); err == nil {
		t.Error("embedded without data dir")
	}
	if _, err := Open(ctx, Options{Mode: ModeExternal}); err == nil {
		t.Error("external without URL")
	}
	if _, err := Open(ctx, Options{Mode: "cloud"}); err == nil {
		t.Error("unknown mode")
	}
	if _, err := Open(ctx, Options{Mode: ModeEmbedded, DataDir: t.TempDir(), Listen: "nonsense"}); err == nil {
		t.Error("bad listen address")
	}
	if _, err := Open(ctx, Options{Mode: ModeExternal, URL: "nats://127.0.0.1:1"}); err == nil {
		t.Error("unreachable external")
	}
}

func TestExternalModeAgainstListeningServer(t *testing.T) {
	ctx := context.Background()
	srv, err := Open(ctx, Options{Mode: ModeEmbedded, NodeID: "srv", DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	url := srv.srv.ClientURL()
	c, err := Open(ctx, Options{Mode: ModeExternal, NodeID: "client", URL: url})
	if err != nil {
		t.Fatalf("external connect: %v", err)
	}
	defer c.Close()
	if err := c.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, Options{Mode: ModeExternal, URL: url, CredsFile: "/does/not/exist.creds"}); err == nil {
		t.Fatal("missing creds file must fail")
	}
}

func TestPublishDedup(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	subj := EventSubject(EventPartition("acc_1", 64), "org_1", "message.received")
	dup, err := b.Publish(ctx, subj, []byte(`{}`), "evt_1", map[string]string{"traceparent": "00-x"})
	if err != nil || dup {
		t.Fatalf("first publish: %v %v", dup, err)
	}
	dup, err = b.Publish(ctx, subj, []byte(`{}`), "evt_1", nil)
	if err != nil || !dup {
		t.Fatalf("second publish must be a duplicate: %v %v", dup, err)
	}
	if _, err := b.Publish(ctx, "nowhere.subject", nil, "", nil); err == nil {
		t.Fatal("publish without a stream must fail")
	}
}

func TestSubjectsAndPartitions(t *testing.T) {
	if EventPartition("", 64) != 0 {
		t.Error("account-less events use partition 0")
	}
	p := EventPartition("acc_01", 64)
	if p < 0 || p >= 64 || p != EventPartition("acc_01", 64) {
		t.Error("partition must be stable and in range")
	}
	if d := DeliveryPartition("whe_1", "acc_1", 256); d < 0 || d >= 256 {
		t.Error("delivery partition range")
	}
	if EventSubject(3, "org_x", "message.received") != "eb.evt.3.org_x.message.received" ||
		DeliverySubject(7) != "eb.dlv.7" || SubmitSubject("acc_1") != "eb.submit.acc_1" ||
		NodeSyncSubject("n1") != "eb.node.n1.sync" || RealtimeSubject("org_1", "accounts.state") != "eb.rt.org_1.accounts.state" {
		t.Error("subject builders")
	}
	for _, bad := range []string{"", "a.b", "a b", "a*", ">"} {
		if ValidToken(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !ValidToken("acc_01jbf3k8v4w7q2m9x5c6t0r1yz") || ErrBadToken == nil {
		t.Error("valid token")
	}
}

func TestLeaseLifecycle(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	ttl := 2 * time.Second
	e1, err := b.Claim(ctx, "acc.1", []byte("n1"), ttl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Claim(ctx, "acc.1", []byte("n2"), ttl); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second claim: %v", err)
	}
	// renewals keep it alive past the TTL
	epoch := e1
	for i := 0; i < 4; i++ {
		time.Sleep(ttl / 3)
		epoch, err = b.Renew(ctx, "acc.1", []byte("n1"), epoch, ttl)
		if err != nil {
			t.Fatalf("renew %d: %v", i, err)
		}
	}
	h, err := b.LeaseHolder(ctx, "acc.1")
	if err != nil || h == nil || string(h.Value) != "n1" || h.Epoch != epoch {
		t.Fatalf("holder = %+v %v", h, err)
	}
	if _, err := b.Renew(ctx, "acc.1", []byte("n1"), e1, ttl); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale renew: %v", err)
	}
	if err := b.Release(ctx, "acc.1", e1); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale release: %v", err)
	}
	if err := b.Release(ctx, "acc.1", epoch); err != nil {
		t.Fatal(err)
	}
	if h, _ := b.LeaseHolder(ctx, "acc.1"); h != nil {
		t.Fatal("released lease must be free")
	}
	// expiry without renewal → another node can claim
	if _, err := b.Claim(ctx, "acc.2", []byte("n1"), ttl); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := b.Claim(ctx, "acc.2", []byte("n2"), ttl); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lease never expired")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if h, _ := b.LeaseHolder(ctx, "acc.2"); h == nil || string(h.Value) != "n2" {
		t.Fatal("takeover holder")
	}
}

func TestDrainingFlagAndRequestReply(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if b.Draining() {
		t.Fatal("a new node is not draining")
	}
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetDraining(ctx, true); err != nil {
		t.Fatal(err)
	}
	nodes, err := b.LiveNodes(ctx)
	if err != nil || len(nodes) != 1 || !nodes[0].Draining || len(nodes[0].Roles) != 1 {
		t.Fatalf("registry after SetDraining: %+v %v", nodes, err)
	}
	// A later heartbeat keeps the flag, even when called with the plain registration info.
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	if nodes, _ = b.LiveNodes(ctx); !nodes[0].Draining || !b.Draining() {
		t.Fatalf("draining lost by the heartbeat: %+v", nodes)
	}
	// Other nodes' entries are written as given.
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: "other", Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	nodes, _ = b.LiveNodes(ctx)
	for _, n := range nodes {
		if n.NodeID == "other" && n.Draining {
			t.Fatal("another node's entry must not inherit this node's flag")
		}
	}

	subject := ClusterDrainSubject("n9")
	unsub, err := b.SubscribeCoreReply(subject, func(_ string, data []byte) []byte {
		if string(data) == "silent" {
			return nil
		}
		return append([]byte("re:"), data...)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unsub() }()
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	got, err := b.RequestCore(rctx, subject, []byte("ping"))
	if err != nil || string(got) != "re:ping" {
		t.Fatalf("request: %q %v", got, err)
	}
	if err := b.PublishCore(subject, []byte("plain publish, no reply wanted")); err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer scancel()
	if _, err := b.RequestCore(sctx, subject, []byte("silent")); err == nil {
		t.Fatal("a handler returning nil sends no reply")
	}
	var nilBus *Bus
	if nilBus.Draining() || nilBus.SetDraining(ctx, true) == nil {
		t.Fatal("nil bus")
	}
}
