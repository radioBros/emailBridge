package authserver

import (
	"sync"
	"time"
)

type cacheEntry struct {
	creds Creds
	until time.Time
}

type memCache struct {
	mu   sync.Mutex
	data map[string]cacheEntry
}

func newMemCache() *memCache {
	return &memCache{data: map[string]cacheEntry{}}
}

func cacheKey(orgID, accountID, protocol string) string {
	return orgID + "|" + accountID + "|" + protocol
}

func (c *memCache) get(key string, now time.Time) (Creds, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok || !e.until.After(now) {
		if ok {
			delete(c.data, key)
		}
		return Creds{}, false
	}
	return e.creds, true
}

func (c *memCache) put(key string, creds Creds, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = cacheEntry{creds: creds, until: until}
}

// Len is for tests.
func (c *memCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.data)
}
