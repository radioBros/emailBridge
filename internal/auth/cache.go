package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/bus"
)

// KV is the cluster-wide cache the auth layer uses: the eb-cache bucket through *bus.Bus (S10 §2.1).
type KV interface {
	CacheGet(ctx context.Context, key string) (bus.CacheEntry, error)
	CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error
	CacheDelete(ctx context.Context, key string) error
	CacheAdd(ctx context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error)
	CacheExpire(ctx context.Context, key string, ttl time.Duration) error
}

var _ KV = (*bus.Bus)(nil)

// limenCache adapts KV to Limen's AtomicCacheAdapter (rate limits, API-key cache and counters), so limits
// hold across every api node.
type limenCache struct {
	kv  KV
	log *slog.Logger
}

var _ limen.AtomicCacheAdapter = (*limenCache)(nil)

// miss maps a cache miss to Limen's not-found error; other errors are logged, because Limen's rate limiter
// turns any store error into a bare 429.
func (c *limenCache) miss(ctx context.Context, op string, err error) error {
	if errors.Is(err, bus.ErrCacheMiss) {
		return limen.ErrRecordNotFound
	}
	c.log.WarnContext(ctx, "auth cache error", "op", op, "err", err)
	return err
}

func (c *limenCache) Get(ctx context.Context, key string) ([]byte, error) {
	e, err := c.kv.CacheGet(ctx, key)
	if err != nil {
		return nil, c.miss(ctx, "get", err)
	}
	return e.Value, nil
}

func (c *limenCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := c.kv.CacheSet(ctx, key, value, ttl); err != nil {
		return c.miss(ctx, "set", err)
	}
	return nil
}

func (c *limenCache) Has(ctx context.Context, key string) (bool, error) {
	_, err := c.kv.CacheGet(ctx, key)
	if errors.Is(err, bus.ErrCacheMiss) {
		return false, nil
	}
	if err != nil {
		return false, c.miss(ctx, "has", err)
	}
	return true, nil
}

func (c *limenCache) Delete(ctx context.Context, key string) error {
	if err := c.kv.CacheDelete(ctx, key); err != nil {
		return c.miss(ctx, "delete", err)
	}
	return nil
}

func (c *limenCache) SetExpiry(ctx context.Context, key string, ttl time.Duration) error {
	if err := c.kv.CacheExpire(ctx, key, ttl); err != nil {
		return c.miss(ctx, "expire", err)
	}
	return nil
}

func (c *limenCache) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	n, err := c.kv.CacheAdd(ctx, key, delta, 0)
	if err != nil {
		return 0, c.miss(ctx, "increment", err)
	}
	return n, nil
}

func (c *limenCache) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	n, err := c.kv.CacheAdd(ctx, key, -delta, 0)
	if err != nil {
		return 0, c.miss(ctx, "decrement", err)
	}
	return n, nil
}
