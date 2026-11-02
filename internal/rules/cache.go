package rules

import "sync"

type cacheKey struct {
	routeID string
	version int
}

type programCache struct {
	mu    sync.Mutex
	size  int
	order []cacheKey
	items map[cacheKey]*Program
}

func newProgramCache(size int) *programCache {
	if size < 1 {
		size = 1
	}
	return &programCache{size: size, items: make(map[cacheKey]*Program)}
}

func (c *programCache) get(routeID string, version int) (*Program, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey{routeID, version}
	p, ok := c.items[k]
	if !ok {
		return nil, false
	}
	// Move to most-recently used.
	c.touch(k)
	return p, true
}

func (c *programCache) put(routeID string, version int, p *Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey{routeID, version}
	if _, ok := c.items[k]; ok {
		c.items[k] = p
		c.touch(k)
		return
	}
	for len(c.order) >= c.size {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.items, old)
	}
	c.items[k] = p
	c.order = append(c.order, k)
}

func (c *programCache) touch(k cacheKey) {
	for i, x := range c.order {
		if x == k {
			c.order = append(append(c.order[:i], c.order[i+1:]...), k)
			return
		}
	}
}

func (c *programCache) remove(routeID string, version int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey{routeID, version}
	delete(c.items, k)
	for i, x := range c.order {
		if x == k {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *programCache) removeRoute(routeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if k.routeID == routeID {
			delete(c.items, k)
		}
	}
	out := c.order[:0]
	for _, k := range c.order {
		if k.routeID != routeID {
			out = append(out, k)
		}
	}
	c.order = out
}
