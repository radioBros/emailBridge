package bus

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCacheSetGetDelete(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	// keys with characters NATS KV keys do not allow
	key := "limen:rl:[2001:db8::1]::/v1/auth/signin/credential"
	if _, err := b.CacheGet(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("missing key: %v", err)
	}
	if err := b.CacheSet(ctx, key, []byte("v1"), 0); err != nil {
		t.Fatal(err)
	}
	e, err := b.CacheGet(ctx, key)
	if err != nil || string(e.Value) != "v1" || !e.ExpiresAt.IsZero() {
		t.Fatalf("get = %+v, %v", e, err)
	}
	if err := b.CacheSet(ctx, key, []byte("v2"), time.Minute); err != nil {
		t.Fatal(err)
	}
	e, _ = b.CacheGet(ctx, key)
	if string(e.Value) != "v2" || time.Until(e.ExpiresAt) < 50*time.Second {
		t.Fatalf("overwrite = %+v", e)
	}
	if err := b.CacheDelete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CacheGet(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("after delete: %v", err)
	}
	if err := b.CacheDelete(ctx, "never-there"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	// a deleted key can be set again
	if err := b.CacheSet(ctx, key, []byte("v3"), 0); err != nil {
		t.Fatal(err)
	}
	if e, _ := b.CacheGet(ctx, key); string(e.Value) != "v3" {
		t.Fatalf("re-set = %q", e.Value)
	}
}

func TestCacheExpiry(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if err := b.CacheSet(ctx, "short", []byte("x"), 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CacheGet(ctx, "short"); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := b.CacheGet(ctx, "short"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("after expiry: %v", err)
	}
	// an expired counter restarts from zero with the new ttl
	n, err := b.CacheAdd(ctx, "short", 5, time.Hour)
	if err != nil || n != 5 {
		t.Fatalf("add after expiry = %d, %v", n, err)
	}
}

func TestCacheAddKeepsExpiry(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	n, err := b.CacheAdd(ctx, "ctr", 1, 10*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("first add = %d, %v", n, err)
	}
	e1, _ := b.CacheGet(ctx, "ctr")
	n, err = b.CacheAdd(ctx, "ctr", 2, time.Hour)
	if err != nil || n != 3 {
		t.Fatalf("second add = %d, %v", n, err)
	}
	e2, _ := b.CacheGet(ctx, "ctr")
	if !e2.ExpiresAt.Equal(e1.ExpiresAt) {
		t.Fatalf("expiry changed: %v → %v", e1.ExpiresAt, e2.ExpiresAt)
	}
	n, err = b.CacheAdd(ctx, "ctr", -4, 0)
	if err != nil || n != -1 {
		t.Fatalf("decrement = %d, %v", n, err)
	}
	if err := b.CacheSet(ctx, "text", []byte("abc"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CacheAdd(ctx, "text", 1, 0); err == nil {
		t.Fatal("adding to a non-integer must fail")
	}
}

func TestCacheExpire(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	if err := b.CacheExpire(ctx, "nope", time.Minute); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expire missing: %v", err)
	}
	if _, err := b.CacheAdd(ctx, "k", 7, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.CacheExpire(ctx, "k", 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	e, err := b.CacheGet(ctx, "k")
	if err != nil || string(e.Value) != "7" || e.ExpiresAt.IsZero() {
		t.Fatalf("after expire = %+v, %v", e, err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := b.CacheGet(ctx, "k"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("after ttl: %v", err)
	}
	if err := b.CacheSet(ctx, "p", []byte("1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := b.CacheExpire(ctx, "p", 0); err != nil {
		t.Fatal(err)
	}
	if e, _ := b.CacheGet(ctx, "p"); !e.ExpiresAt.IsZero() {
		t.Fatalf("persist: %+v", e)
	}
}

func TestCacheAddConcurrent(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for range workers {
		wg.Go(func() {
			for range each {
				if _, err := b.CacheAdd(ctx, "race", 1, time.Minute); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	e, err := b.CacheGet(ctx, "race")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := strconv.Atoi(string(e.Value)); got != workers*each {
		t.Fatalf("counter = %d, want %d", got, workers*each)
	}
}

func TestCacheCodecAndTTL(t *testing.T) {
	if _, err := decodeCacheValue([]byte{1, 2}); err == nil {
		t.Fatal("short entry must fail")
	}
	now := time.Now()
	if msgTTL(time.Time{}, now) != 0 {
		t.Fatal("no expiry → no ttl")
	}
	if msgTTL(now.Add(10*time.Millisecond), now) != time.Second {
		t.Fatal("sub-second ttl rounds up to 1s")
	}
	if got := msgTTL(now.Add(1500*time.Millisecond), now); got != 2*time.Second {
		t.Fatalf("ttl = %v", got)
	}
	if cacheKey("a") == cacheKey("b") || len(cacheKey("x")) != 43 {
		t.Fatal("cache key encoding")
	}
}

func TestCacheErrorsAfterClose(t *testing.T) {
	b, err := Open(context.Background(), Options{Mode: ModeEmbedded, NodeID: "t2", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := b.CacheGet(ctx, "k"); err == nil || errors.Is(err, ErrCacheMiss) {
		t.Fatalf("get on closed bus: %v", err)
	}
	if err := b.CacheSet(ctx, "k", nil, 0); err == nil {
		t.Fatal("set on closed bus")
	}
	if err := b.CacheDelete(ctx, "k"); err == nil {
		t.Fatal("delete on closed bus")
	}
	if _, err := b.CacheAdd(ctx, "k", 1, 0); err == nil {
		t.Fatal("add on closed bus")
	}
	if err := b.CacheExpire(ctx, "k", 0); err == nil {
		t.Fatal("expire on closed bus")
	}
}
