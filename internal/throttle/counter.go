package throttle

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Counter is the cluster-wide approximate counter store (NATS eb-cache in production).
type Counter interface {
	Add(ctx context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error)
	Get(ctx context.Context, key string) (int64, error)
}

// MemoryCounter is an in-process Counter for tests (shared across "nodes" in the same process).
type MemoryCounter struct {
	mu   sync.Mutex
	vals map[string]memEntry
}

type memEntry struct {
	n   int64
	exp time.Time
}

// NewMemoryCounter returns an empty MemoryCounter.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{vals: map[string]memEntry{}}
}

// Add implements Counter.
func (m *MemoryCounter) Add(_ context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	e, ok := m.vals[key]
	if !ok || (!e.exp.IsZero() && !now.Before(e.exp)) {
		e = memEntry{}
		if ttlIfNew > 0 {
			e.exp = now.Add(ttlIfNew)
		}
	}
	e.n += delta
	m.vals[key] = e
	return e.n, nil
}

// Get implements Counter.
func (m *MemoryCounter) Get(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	e, ok := m.vals[key]
	if !ok || (!e.exp.IsZero() && !now.Before(e.exp)) {
		return 0, nil
	}
	return e.n, nil
}

// BusCounter adapts bus cache helpers. Callers pass the methods to avoid importing nats in tests.
type BusCounter struct {
	AddFn func(ctx context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error)
	GetFn func(ctx context.Context, key string) (int64, error)
}

// Add implements Counter.
func (b BusCounter) Add(ctx context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error) {
	return b.AddFn(ctx, key, delta, ttlIfNew)
}

// Get implements Counter.
func (b BusCounter) Get(ctx context.Context, key string) (int64, error) {
	return b.GetFn(ctx, key)
}

// ParseIntBytes parses a decimal counter value from cache bytes.
func ParseIntBytes(v []byte) (int64, error) {
	return strconv.ParseInt(string(v), 10, 64)
}
