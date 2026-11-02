package sinks

import (
	"fmt"
	"sync"
)

// Cache reuses Sink clients keyed by endpoint ID + config version.
type Cache struct {
	deps Deps
	mu   sync.Mutex
	m    map[string]Sink
}

// NewCache returns an empty client cache.
func NewCache(deps Deps) *Cache {
	return &Cache{deps: deps, m: map[string]Sink{}}
}

func cacheKey(endpointID string, version int64) string {
	return fmt.Sprintf("%s\x00%d", endpointID, version)
}

// Get returns a cached sink or creates one with New using the cache's deps.
func (c *Cache) Get(kind string, cfg Config) (Sink, error) {
	if c == nil {
		return New(kind, cfg, Deps{})
	}
	return c.GetWithDeps(kind, cfg, c.deps)
}

// GetWithDeps returns a cached sink or creates one with the provided deps (e.g. ambient flag).
func (c *Cache) GetWithDeps(kind string, cfg Config, deps Deps) (Sink, error) {
	if c == nil {
		return New(kind, cfg, deps)
	}
	key := cacheKey(cfg.EndpointID, cfg.Version)
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.m[key]; ok {
		return s, nil
	}
	s, err := New(kind, cfg, deps)
	if err != nil {
		return nil, err
	}
	c.m[key] = s
	return s, nil
}

// Evict closes and removes the sink for endpointID+version.
func (c *Cache) Evict(endpointID string, version int64) {
	if c == nil {
		return
	}
	key := cacheKey(endpointID, version)
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.m[key]; ok {
		delete(c.m, key)
		_ = s.Close()
	}
}

// Close closes every cached sink. Idempotent.
func (c *Cache) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	for k, s := range c.m {
		delete(c.m, k)
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
