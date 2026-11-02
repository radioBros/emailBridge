package bus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowLeases is an in-memory lease store whose every round trip takes delay (a loaded NATS server).
type slowLeases struct {
	delay  time.Duration
	mu     sync.Mutex
	epochs map[string]uint64
	seq    uint64
	lost   atomic.Int32
	active atomic.Int32
	peak   atomic.Int32
}

func (s *slowLeases) wait() {
	n := s.active.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(s.delay)
	s.active.Add(-1)
}

func (s *slowLeases) Claim(_ context.Context, key string, _ []byte, _ time.Duration) (uint64, error) {
	s.wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epochs[key]; ok {
		return 0, ErrLeaseHeld
	}
	s.seq++
	s.epochs[key] = s.seq
	return s.seq, nil
}

func (s *slowLeases) Renew(_ context.Context, key string, _ []byte, epoch uint64, _ time.Duration) (uint64, error) {
	s.wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epochs[key] != epoch {
		s.lost.Add(1)
		return 0, ErrLeaseLost
	}
	s.seq++
	s.epochs[key] = s.seq
	return s.seq, nil
}

func (s *slowLeases) Release(_ context.Context, key string, _ uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.epochs, key)
	return nil
}

func (s *slowLeases) NodeIDs(context.Context) ([]string, error) { return []string{"n1"}, nil }

// A node holding hundreds of partitions must renew them all well inside the TTL even when each round trip is
// slow: done one by one, 256 × 40 ms = 10 s would outlast a 1 s TTL and every partition would restart.
func TestBalancerRenewsManyPartitionsWithinTTL(t *testing.T) {
	leases := &slowLeases{delay: 40 * time.Millisecond, epochs: map[string]uint64{}}
	var starts atomic.Int32
	b := &Balancer{
		Leases: leases, Prefix: "dlv", N: 256, NodeID: "n1", TTL: time.Second,
		Work: func(ctx context.Context, _ int, _ uint64) {
			starts.Add(1)
			<-ctx.Done()
		},
	}
	ctx := context.Background()
	b.tick(ctx) // claims all 256
	if got := len(b.Held()); got != 256 {
		t.Fatalf("held %d partitions after the first tick, want 256", got)
	}
	for range 3 {
		began := time.Now()
		b.tick(ctx)
		if d := time.Since(began); d > b.ttl()/2 {
			t.Fatalf("renewing 256 leases took %s, want under half the %s TTL", d, b.ttl())
		}
	}
	if n := leases.lost.Load(); n != 0 {
		t.Fatalf("%d leases lost", n)
	}
	if n := starts.Load(); n != 256 {
		t.Fatalf("work started %d times, want 256 (no restarts)", n)
	}
	if p := leases.peak.Load(); p > leaseOpsParallel {
		t.Fatalf("peak concurrent lease calls %d, want at most %d", p, leaseOpsParallel)
	}
	b.releaseAll(ctx)
	if got := len(b.Held()); got != 0 {
		t.Fatalf("held %d after releaseAll", got)
	}
}
