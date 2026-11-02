package imapproxy

import (
	"sync"
	"time"
)

type limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	start time.Time
	n     int
}

func newLimiter(perMinute int) *limiter {
	if perMinute <= 0 {
		perMinute = 10
	}
	return &limiter{
		limit: perMinute, window: time.Minute,
		buckets: map[string]*bucket{}, now: time.Now,
	}
}

func (l *limiter) allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= l.window {
		l.buckets[key] = &bucket{start: now, n: 1}
		return true
	}
	if b.n >= l.limit {
		return false
	}
	b.n++
	return true
}
