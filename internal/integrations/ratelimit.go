package integrations

import (
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// callbackLimiter is a simple per-key token bucket (60/min default).
type callbackLimiter struct {
	mu   sync.Mutex
	rate float64 // tokens per second
	cap  float64
	buck map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newCallbackLimiter(perMinute float64) *callbackLimiter {
	if perMinute <= 0 {
		perMinute = 60
	}
	return &callbackLimiter{
		rate: perMinute / 60, cap: perMinute, buck: map[string]*bucket{},
	}
}

func (l *callbackLimiter) take(key string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buck[key]
	if b == nil {
		b = &bucket{tokens: l.cap, last: now}
		l.buck[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.cap {
		b.tokens = l.cap
	}
	b.last = now
	if b.tokens < 1 {
		return errs.New(errs.CodeRateLimited, "Too many integration callbacks; try again later.")
	}
	b.tokens--
	return nil
}
