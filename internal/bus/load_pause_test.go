package bus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeLoadInRegistry(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	b.SetAccountsProvider(func() int { return 3 })
	b.SetPartitionLoad("evt", 2)
	b.SetPartitionLoad("dlv", 5)
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"sync", "webhook"}}); err != nil {
		t.Fatal(err)
	}
	nodes, err := b.LiveNodes(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes: %+v %v", nodes, err)
	}
	n := nodes[0]
	if n.Accounts != 3 || n.Partitions["evt"] != 2 || n.Partitions["dlv"] != 5 {
		t.Fatalf("load: %+v", n)
	}
	b.SetLoadProvider(func() (int, map[string]int) { return 9, map[string]int{"relay": 1} })
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	nodes, _ = b.LiveNodes(ctx)
	if nodes[0].Accounts != 9 || nodes[0].Partitions["relay"] != 1 || nodes[0].Partitions["evt"] != 0 {
		t.Fatalf("load provider override: %+v", nodes[0])
	}
}

func TestBalancerReportsPartitionLoad(t *testing.T) {
	b := openEmbedded(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"webhook"}}); err != nil {
		t.Fatal(err)
	}
	var held atomic.Int32
	bal := &Balancer{
		Leases: b, Prefix: "evt", Roles: []string{"webhook"}, N: 4, NodeID: b.o.NodeID,
		TTL: time.Second, Work: func(context.Context, int, uint64) {},
		OnChange: func(n int) { held.Store(int32(n)) },
	}
	go bal.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if held.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if held.Load() == 0 {
		t.Fatal("balancer never held partitions")
	}
	if err := b.RegisterNode(ctx, NodeInfo{NodeID: b.o.NodeID, Roles: []string{"webhook"}}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := b.LiveNodes(ctx)
	if nodes[0].Partitions["evt"] == 0 {
		t.Fatalf("partition load missing: %+v", nodes[0])
	}
}

func TestConsumePartitionHonoursPause(t *testing.T) {
	b := openEmbedded(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	stop, err := b.ConsumePartition(ctx, StreamSubmit, "submit-pause", SubmitSubject("*"), 1, func(context.Context, Message) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	b.SetQueuePaused(StreamSubmit, true)
	if _, err := b.Publish(ctx, SubmitSubject("acc_1"), []byte(`{}`), "pause-1", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("handler ran while paused: %d", calls.Load())
	}

	b.SetQueuePaused(StreamSubmit, false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("handler did not run after unpause")
	}
}

func TestPublishQueuePausedFansOut(t *testing.T) {
	b := openEmbedded(t)
	unsub, err := b.watchQueueControl()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unsub() }()
	if err := b.PublishQueuePaused(StreamDelivery, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !b.QueuePaused(StreamDelivery) {
		time.Sleep(20 * time.Millisecond)
	}
	if !b.QueuePaused(StreamDelivery) {
		t.Fatal("pause not applied")
	}
	st, err := b.QueueStats(context.Background(), StreamDelivery)
	if err != nil || !st.Paused {
		t.Fatalf("stats: %+v %v", st, err)
	}
}

func TestParseKeep(t *testing.T) {
	d, err := parseKeep("7d")
	if err != nil || d != 7*24*time.Hour {
		t.Fatalf("7d: %v %v", d, err)
	}
	if formatKeep(24*time.Hour) != "1d" || formatKeep(2*time.Hour) != "2h" || formatKeep(5*time.Minute) != "5m" {
		t.Fatal(formatKeep(24*time.Hour), formatKeep(2*time.Hour), formatKeep(5*time.Minute))
	}
	if formatKeep(0) != "" || formatKeep(1500*time.Millisecond) == "" {
		t.Fatal("edge format")
	}
	if _, err := parseKeep(""); err == nil {
		t.Fatal("empty keep")
	}
	if _, err := parseKeep("-1d"); err == nil {
		t.Fatal("negative day")
	}
	if _, err := parseKeep("-5s"); err == nil {
		t.Fatal("negative duration")
	}
	if _, err := parseKeep("not-a-duration"); err == nil {
		t.Fatal("garbage")
	}
	if d, err := parseKeep("90m"); err != nil || d != 90*time.Minute {
		t.Fatal(d, err)
	}
}

func TestSetStreamMaxAgeAndWorkStreams(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if len(WorkStreams()) != 4 {
		t.Fatal(WorkStreams())
	}
	if err := b.SetStreamMaxAge(ctx, StreamEvents, "2d"); err != nil {
		t.Fatal(err)
	}
	st, err := b.QueueStats(ctx, StreamEvents)
	if err != nil || st.Keep != "2d" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := b.SetStreamMaxAge(ctx, StreamEvents, "bad"); err == nil {
		t.Fatal("bad keep accepted")
	}
	if err := (*Bus)(nil).SetStreamMaxAge(ctx, StreamEvents, "1d"); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	if _, err := (*Bus)(nil).QueueStats(ctx, StreamEvents); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	if err := (*Bus)(nil).PublishQueuePaused(StreamEvents, true); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	(*Bus)(nil).SetLoadProvider(nil)
	(*Bus)(nil).SetAccountsProvider(nil)
	(*Bus)(nil).SetPartitionLoad("x", 1)
	(*Bus)(nil).SetQueuePaused("x", true)
	if (*Bus)(nil).QueuePaused("x") {
		t.Fatal("nil paused")
	}
	b.SetPartitionLoad("evt", 0) // delete
	b.SetQueuePaused(StreamEvents, true)
	b.SetQueuePaused(StreamEvents, true) // no-op same state
	b.SetQueuePaused(StreamEvents, false)
}

func TestWatchQueueControlIgnoresBadPayload(t *testing.T) {
	b := openEmbedded(t)
	unsub, err := b.watchQueueControl()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unsub() }()
	_ = b.PublishCore(QueueControlSubject, []byte(`{`))
	_ = b.PublishCore(QueueControlSubject, []byte(`{"queue":"","paused":true}`))
	_ = b.PublishQueuePaused(StreamSysmail, true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !b.QueuePaused(StreamSysmail) {
		time.Sleep(20 * time.Millisecond)
	}
	if !b.QueuePaused(StreamSysmail) {
		t.Fatal("sysmail pause")
	}
}
