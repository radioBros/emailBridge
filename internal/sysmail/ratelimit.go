package sysmail

import (
	"strings"
	"sync"
	"time"
)

// Per-recipient rate limit for P0: in-memory token bucket on this node only (S10 §7).
// Cluster-wide limiting via NATS KV is deferred; document in Module godoc.
const (
	rateLimitPerHour = 10
	rateLimitWindow  = time.Hour
)

type rateLimiter struct {
	mu   sync.Mutex
	byTo map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{byTo: map[string]*bucket{}}
}

// allow reports whether one more send to addr is permitted and consumes a token when true.
func (r *rateLimiter) allow(addr string, now time.Time) bool {
	key := strings.ToLower(strings.TrimSpace(addr))
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.byTo[key]
	if b == nil {
		b = &bucket{tokens: rateLimitPerHour, last: now}
		r.byTo[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * (float64(rateLimitPerHour) / rateLimitWindow.Seconds())
	if b.tokens > rateLimitPerHour {
		b.tokens = rateLimitPerHour
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
