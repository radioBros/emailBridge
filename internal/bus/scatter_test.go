package bus

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func TestScatterGatherCollectsUntilExpectOrDeadline(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	const subj = "eb.test.scatter"
	for _, name := range []string{"a", "b"} {
		unsub, err := b.Respond(subj, func(_ context.Context, req []byte) []byte { return append([]byte(name+":"), req...) })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(unsub)
	}
	var slowRunning atomic.Int32
	unsubSlow, err := b.Respond(subj, func(ctx context.Context, _ []byte) []byte {
		slowRunning.Add(1)
		defer slowRunning.Add(-1)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		return []byte("slow")
	})
	if err != nil {
		t.Fatal(err)
	}
	// a responder that never answers (nil reply)
	unsubSilent, err := b.Respond(subj, func(context.Context, []byte) []byte { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer unsubSilent()

	start := time.Now()
	got, err := b.ScatterGather(ctx, subj, []byte("q"), 2, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || time.Since(start) > time.Second {
		t.Fatalf("expect=2 should return early with 2 replies, got %d after %v", len(got), time.Since(start))
	}
	var s []string
	for _, g := range got {
		s = append(s, string(g))
	}
	sort.Strings(s)
	if !slices.Equal(s, []string{"a:q", "b:q"}) {
		t.Fatalf("replies %v", s)
	}

	start = time.Now()
	got, err = b.ScatterGather(ctx, subj, []byte("q"), 4, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("deadline: %d replies after %v", len(got), time.Since(start))
	}
	// unsubscribing cancels the slow handler's context and waits for it
	unsubSlow()
	if slowRunning.Load() != 0 {
		t.Fatal("unsubscribe must wait for running handlers")
	}
	unsubSlow() // idempotent

	// ctx cancellation ends the gather without error
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	got, err = b.ScatterGather(cctx, subj, []byte("q"), 0, 5*time.Second)
	if err != nil || len(got) != 2 {
		t.Fatalf("ctx end: %d %v", len(got), err)
	}
	if _, err := b.ScatterGather(ctx, subj, nil, 1, 0); err == nil {
		t.Fatal("zero deadline must fail")
	}
}

func TestRespondBoundsConcurrency(t *testing.T) {
	b := openEmbedded(t)
	var running, peak atomic.Int32
	unsub, err := b.Respond("eb.test.conc", func(context.Context, []byte) []byte {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		running.Add(-1)
		return []byte("ok")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	done := make(chan int, 20)
	for i := 0; i < 20; i++ {
		go func() {
			got, _ := b.ScatterGather(context.Background(), "eb.test.conc", nil, 1, 5*time.Second)
			done <- len(got)
		}()
	}
	for i := 0; i < 20; i++ {
		if n := <-done; n != 1 {
			t.Fatalf("request %d got %d replies", i, n)
		}
	}
	if p := peak.Load(); p < 2 || p > respondConcurrency {
		t.Fatalf("peak concurrency %d, want 2..%d", p, respondConcurrency)
	}
}

func TestMaxPayloadAndCoreFeed(t *testing.T) {
	b := openEmbedded(t)
	got := make(chan string, 4)
	unsub, err := b.SubscribeCore("eb.test.tail", func(_ string, d []byte) { got <- string(d) })
	if err != nil {
		t.Fatal(err)
	}
	if err := b.PublishCore("eb.test.tail", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "hello" {
			t.Fatalf("got %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message")
	}
	_ = unsub()
	if !b.Embedded() {
		t.Fatal("embedded bus")
	}
	if b.MaxPayload() < 1<<20 {
		t.Fatalf("max payload %d", b.MaxPayload())
	}
}

func TestNodeIDs(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	ids, err := b.NodeIDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("empty registry: %v %v", ids, err)
	}
	for _, n := range []string{"n2", "n1"} {
		if err := b.RegisterNode(ctx, NodeInfo{NodeID: n}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err = b.NodeIDs(ctx)
	if err != nil || !slices.Equal(ids, []string{"n1", "n2"}) {
		t.Fatalf("ids %v %v", ids, err)
	}
}

func TestCoreHelpersWithoutConnection(t *testing.T) {
	var nilBus *Bus
	ctx := context.Background()
	if _, err := nilBus.ScatterGather(ctx, "x", nil, 1, time.Second); !errors.Is(err, ErrNotConnected) {
		t.Errorf("scatter: %v", err)
	}
	if _, err := nilBus.Respond("x", nil); !errors.Is(err, ErrNotConnected) {
		t.Errorf("respond: %v", err)
	}
	if _, err := nilBus.NodeIDs(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("nodes: %v", err)
	}
	if nilBus.Embedded() {
		t.Error("nil bus is not embedded")
	}
	if nilBus.MaxPayload() != 1<<20 {
		t.Error("default max payload")
	}
	b, err := Open(ctx, Options{Mode: ModeEmbedded, NodeID: "c", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if _, err := b.Respond("x", nil); !errors.Is(err, ErrNotConnected) {
		t.Errorf("closed respond: %v", err)
	}
	if _, err := b.ScatterGather(ctx, "x", nil, 1, time.Second); !errors.Is(err, ErrNotConnected) {
		t.Errorf("closed scatter: %v", err)
	}
}

func TestScatterGatherWithoutRespondersReturnsAtOnce(t *testing.T) {
	b := openEmbedded(t)
	start := time.Now()
	got, err := b.ScatterGather(context.Background(), "eb.test.nobody", []byte("q"), 2, 3*time.Second)
	if err != nil || len(got) != 0 || time.Since(start) > time.Second {
		t.Fatalf("no responders: %d %v after %v", len(got), err, time.Since(start))
	}
}

func TestRespondIgnoresMessagesWithoutReply(t *testing.T) {
	b := openEmbedded(t)
	var calls atomic.Int32
	unsub, err := b.Respond("eb.test.noreply", func(context.Context, []byte) []byte {
		calls.Add(1)
		return []byte("x")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	if err := b.PublishCore("eb.test.noreply", []byte("fire and forget")); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.ScatterGather(context.Background(), "eb.test.noreply", nil, 1, 2*time.Second); len(got) != 1 {
		t.Fatal("request must be answered")
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times; a message without reply subject must be ignored", calls.Load())
	}
}
