package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
)

func TestElectorSingleLeaderAndFailover(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "t", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	var leaders atomic.Int32
	var maxLeaders atomic.Int32
	var mu sync.Mutex
	led := map[string]bool{}
	mk := func(id string) *Elector {
		return &Elector{Leases: b, NodeID: id, TTL: 3 * time.Second, Log: quiet,
			Lead: func(ctx context.Context) {
				n := leaders.Add(1)
				for {
					m := maxLeaders.Load()
					if n <= m || maxLeaders.CompareAndSwap(m, n) {
						break
					}
				}
				mu.Lock()
				led[id] = true
				mu.Unlock()
				<-ctx.Done()
				leaders.Add(-1)
			},
		}
	}
	ctx1, stop1 := context.WithCancel(context.Background())
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	var changes atomic.Int32
	e1, e2 := mk("n1"), mk("n2")
	e1.OnChange = func(bool) { changes.Add(1) }
	done1 := make(chan struct{})
	go func() { e1.Run(ctx1); close(done1) }()
	time.Sleep(500 * time.Millisecond)
	go e2.Run(ctx2)
	time.Sleep(3 * time.Second)
	mu.Lock()
	n1, n2 := led["n1"], led["n2"]
	mu.Unlock()
	if maxLeaders.Load() != 1 || !n1 || n2 {
		t.Fatalf("exactly n1 must lead: max=%d n1=%v n2=%v", maxLeaders.Load(), n1, n2)
	}
	// n1 stops: it releases, n2 takes over within one retry interval
	stop1()
	<-done1
	if changes.Load() != 2 {
		t.Fatalf("OnChange calls = %d", changes.Load())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		ok := led["n2"]
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("n2 never became leader")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if maxLeaders.Load() != 1 {
		t.Fatalf("two leaders at once: %d", maxLeaders.Load())
	}
}

type lossyLeases struct {
	renewErr error
	claims   atomic.Int32
}

func (l *lossyLeases) Claim(context.Context, string, []byte, time.Duration) (uint64, error) {
	l.claims.Add(1)
	return 1, nil
}
func (l *lossyLeases) Renew(context.Context, string, []byte, uint64, time.Duration) (uint64, error) {
	return 0, l.renewErr
}
func (l *lossyLeases) Release(context.Context, string, uint64) error { return nil }

func TestElectorStopsLeadingWhenRenewFails(t *testing.T) {
	l := &lossyLeases{renewErr: bus.ErrLeaseLost}
	var stopped atomic.Int32
	e := &Elector{Leases: l, NodeID: "n", TTL: 300 * time.Millisecond, Lead: func(ctx context.Context) {
		<-ctx.Done()
		stopped.Add(1)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	e.Run(ctx)
	if stopped.Load() < 2 || l.claims.Load() < 2 {
		t.Fatalf("lead must stop on lost renewal and be re-claimed: stopped=%d claims=%d", stopped.Load(), l.claims.Load())
	}
	if (&Elector{}).ttl() != 15*time.Second {
		t.Fatal("default ttl")
	}
}
