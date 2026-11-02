package autoconfig

import (
	"sync"
	"time"
)

type cacheEntry struct {
	result Result
	until  time.Time
}

type resultCache struct {
	mu   sync.Mutex
	data map[string]cacheEntry
	now  func() time.Time
	ttl  time.Duration
}

func newCache(ttl time.Duration) *resultCache {
	return &resultCache{data: map[string]cacheEntry{}, now: time.Now, ttl: ttl}
}

func (c *resultCache) get(domain string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[domain]
	if !ok || c.now().After(e.until) {
		if ok {
			delete(c.data, domain)
		}
		return Result{}, false
	}
	return e.result, true
}

func (c *resultCache) put(domain string, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[domain] = cacheEntry{result: r, until: c.now().Add(c.ttl)}
}
