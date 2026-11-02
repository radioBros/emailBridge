package bus

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// PartitionLeases is the part of *Bus a Balancer needs (a fake in tests).
type PartitionLeases interface {
	Claim(ctx context.Context, key string, value []byte, ttl time.Duration) (uint64, error)
	Renew(ctx context.Context, key string, value []byte, epoch uint64, ttl time.Duration) (uint64, error)
	Release(ctx context.Context, key string, epoch uint64) error
	NodeIDs(ctx context.Context) ([]string, error)
}

// nodeLister is implemented by *Bus: the registry entries with roles and the draining flag.
type nodeLister interface {
	LiveNodes(ctx context.Context) ([]NodeInfo, error)
}

// Balancer spreads N partition leases "<Prefix>.<p>" over the live nodes (rendezvous hashing over the
// eb-nodes registry) and runs Work for every partition this node holds. When a node joins or leaves, the
// partitions whose preferred owner changed are released by the old holder and claimed by the new one;
// a dead node's leases expire after TTL. Work must stop promptly when its ctx is cancelled (lease lost,
// hand-off or shutdown); the lease is released only after Work returned, so two nodes never work one
// partition at the same time while their leases are valid.
type Balancer struct {
	Leases PartitionLeases
	Prefix string // e.g. "relay", "evt", "dlv"
	// Roles limits placement to live, non-draining nodes that run at least one of these roles — exactly the
	// nodes that run Work. With roles split over processes, a partition placed on a node that does not run
	// the work would never be consumed. Empty = every live node (only for work every node runs).
	Roles  []string
	N      int
	NodeID string
	TTL    time.Duration // default 15 s, whole seconds (≥ 1 s), renewed every TTL/3
	Log    *slog.Logger
	Work   func(ctx context.Context, partition int, epoch uint64)
	// OnChange reports the number of held partitions after each change (metrics).
	OnChange func(held int)

	mu   sync.Mutex
	held map[int]*heldPartition
	last []string
}

type heldPartition struct {
	epoch  uint64
	cancel context.CancelFunc
	done   chan struct{}
}

func (b *Balancer) key(p int) string { return fmt.Sprintf("%s.%d", b.Prefix, p) }

func (b *Balancer) ttl() time.Duration {
	switch {
	case b.TTL <= 0:
		return 15 * time.Second
	case b.TTL < time.Second: // NATS per-message TTLs have one-second granularity
		return time.Second
	}
	return b.TTL.Truncate(time.Second)
}

// Owner is the preferred node of partition p among nodes (highest rendezvous score).
func Owner(prefix string, p int, nodes []string) string {
	best, bestScore := "", uint64(0)
	for _, n := range nodes {
		h := fnv.New64a()
		_, _ = fmt.Fprintf(h, "%s/%s.%d", n, prefix, p)
		if s := fmix64(h.Sum64()); best == "" || s > bestScore || (s == bestScore && n < best) {
			best, bestScore = n, s
		}
	}
	return best
}

// fmix64 is murmur3's finaliser: FNV-1a alone mixes near-identical inputs poorly in the high bits,
// which skews rendezvous winners.
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// Held returns the partitions this node holds now.
func (b *Balancer) Held() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int, 0, len(b.held))
	for p := range b.held {
		out = append(out, p)
	}
	return out
}

// Run balances until ctx ends, then stops every Work and releases every lease.
func (b *Balancer) Run(ctx context.Context) {
	b.init()
	t := time.NewTicker(b.ttl() / 3)
	defer t.Stop()
	for {
		b.tick(ctx)
		select {
		case <-ctx.Done():
			b.releaseAll(context.WithoutCancel(ctx))
			return
		case <-t.C:
		}
	}
}

func (b *Balancer) nodes(ctx context.Context) []string {
	ids, err := b.candidates(ctx)
	if err != nil {
		ids = b.last // keep the last view while the registry is unreachable
	} else {
		b.last = ids
	}
	for _, id := range ids {
		if id == b.NodeID {
			return ids
		}
	}
	return append(append([]string(nil), ids...), b.NodeID) // this node is alive even before its first heartbeat lands
}

// candidates are the registry's node IDs, limited to Roles (and to non-draining nodes) when set.
func (b *Balancer) candidates(ctx context.Context) ([]string, error) {
	nl, ok := b.Leases.(nodeLister)
	if len(b.Roles) == 0 || !ok {
		return b.Leases.NodeIDs(ctx)
	}
	infos, err := nl.LiveNodes(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(infos))
	for _, n := range infos {
		if !n.Draining && slices.ContainsFunc(b.Roles, func(r string) bool { return slices.Contains(n.Roles, r) }) {
			ids = append(ids, n.NodeID)
		}
	}
	return ids, nil
}

func (b *Balancer) init() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Log == nil {
		b.Log = slog.Default()
	}
	if b.held == nil {
		b.held = map[int]*heldPartition{}
	}
}

// leaseOpsParallel bounds the lease round trips one tick runs at once. A webhook node holds hundreds of
// partitions (64 event + 256 delivery by default); renewing them one by one made a tick last N × the NATS
// round trip, which under load outlasted the TTL, so every lease expired and every consumer restarted at once
// (a ~40 s delivery stall in the 1 000-account cluster load test).
const leaseOpsParallel = 32

type heldRef struct {
	p int
	h *heldPartition
}

func (b *Balancer) tick(ctx context.Context) {
	b.init()
	if ctx.Err() != nil {
		return
	}
	nodes := b.nodes(ctx)
	value := []byte(b.NodeID)
	var renew, handoff []heldRef
	var claim []int
	b.mu.Lock()
	for p := 0; p < b.N; p++ {
		want := Owner(b.Prefix, p, nodes) == b.NodeID
		h := b.held[p]
		switch {
		case h != nil && !want:
			handoff = append(handoff, heldRef{p, h})
		case h != nil:
			renew = append(renew, heldRef{p, h})
		case want:
			claim = append(claim, p)
		}
	}
	b.mu.Unlock()

	// 1. Renew every lease this node keeps, first and concurrently, so neither the number of partitions nor a
	// partition that is still draining can push a renewal past the TTL.
	var lostMu sync.Mutex
	var lost []heldRef
	parallel(len(renew), func(i int) {
		r := renew[i]
		epoch, err := b.Leases.Renew(ctx, b.key(r.p), value, r.h.epoch, b.ttl())
		if err != nil {
			if ctx.Err() == nil {
				b.Log.Warn("partition lease lost", "lease", b.key(r.p), "err", err)
			}
			lostMu.Lock()
			lost = append(lost, r)
			lostMu.Unlock()
			return
		}
		r.h.epoch = epoch
	})
	if ctx.Err() != nil {
		return // Run releases everything on the way out
	}

	// 2. Stop the partitions whose lease is gone and hand off the ones another node now prefers.
	parallel(len(lost), func(i int) { b.stop(lost[i].p, lost[i].h) })
	parallel(len(handoff), func(i int) { b.release(ctx, handoff[i].p, handoff[i].h, "hand-off") })

	// 3. Claim the partitions this node prefers and does not hold yet.
	var claimed atomic.Int32
	parallel(len(claim), func(i int) {
		p := claim[i]
		epoch, err := b.Leases.Claim(ctx, b.key(p), value, b.ttl())
		if err != nil {
			if !errors.Is(err, ErrLeaseHeld) && ctx.Err() == nil {
				b.Log.Warn("partition claim failed", "lease", b.key(p), "err", err)
			}
			return
		}
		b.start(ctx, p, epoch)
		claimed.Add(1)
	})

	if len(lost) > 0 || len(handoff) > 0 || claimed.Load() > 0 {
		held := len(b.Held())
		if b.OnChange != nil {
			b.OnChange(held)
		}
		if sink, ok := b.Leases.(interface{ SetPartitionLoad(prefix string, n int) }); ok {
			sink.SetPartitionLoad(b.Prefix, held)
		}
	}
}

// parallel runs f(0..n-1) on at most leaseOpsParallel goroutines and waits for all of them.
func parallel(n int, f func(i int)) {
	if n == 0 {
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(n, leaseOpsParallel) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				f(i)
			}
		}()
	}
	wg.Wait()
}

func (b *Balancer) start(ctx context.Context, p int, epoch uint64) {
	wctx, cancel := context.WithCancel(ctx)
	h := &heldPartition{epoch: epoch, cancel: cancel, done: make(chan struct{})}
	b.mu.Lock()
	b.held[p] = h
	b.mu.Unlock()
	go func() {
		defer close(h.done)
		b.Work(wctx, p, epoch)
	}()
}

// stop cancels Work and forgets the partition without releasing (the lease is already gone).
func (b *Balancer) stop(p int, h *heldPartition) {
	h.cancel()
	<-h.done
	b.mu.Lock()
	delete(b.held, p)
	b.mu.Unlock()
}

func (b *Balancer) release(ctx context.Context, p int, h *heldPartition, why string) {
	b.stop(p, h)
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := b.Leases.Release(rctx, b.key(p), h.epoch); err != nil && !errors.Is(err, ErrLeaseLost) {
		b.Log.Warn("partition release failed; it expires after the TTL", "lease", b.key(p), "reason", why, "err", err)
	}
}

func (b *Balancer) releaseAll(ctx context.Context) {
	b.mu.Lock()
	held := make(map[int]*heldPartition, len(b.held))
	for p, h := range b.held {
		held[p] = h
	}
	b.mu.Unlock()
	var wg sync.WaitGroup
	for p, h := range held {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.release(ctx, p, h, "shutdown")
		}()
	}
	wg.Wait()
}

// OwnerOf is the preferred node for an arbitrary key (e.g. "acc.<accountId>") by the same rendezvous
// hashing as Owner.
func OwnerOf(key string, nodes []string) string {
	best, bestScore := "", uint64(0)
	for _, n := range nodes {
		h := fnv.New64a()
		_, _ = fmt.Fprintf(h, "%s/%s", n, key)
		if s := fmix64(h.Sum64()); best == "" || s > bestScore || (s == bestScore && n < best) {
			best, bestScore = n, s
		}
	}
	return best
}
