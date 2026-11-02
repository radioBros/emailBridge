package authserver

import (
	"sync"
	"time"
)

// Simple consecutive-failure breaker (in-memory per organisation URL).
type breaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	threshold int
	cooldown  time.Duration
}

func newBreaker() *breaker {
	return &breaker{threshold: 5, cooldown: 30 * time.Second}
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.openUntil)
}

func (b *breaker) record(now time.Time, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.failures = 0
		b.openUntil = time.Time{}
		return
	}
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = now.Add(b.cooldown)
		b.failures = 0
	}
}
