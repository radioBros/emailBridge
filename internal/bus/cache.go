package bus

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrCacheMiss is returned by the cache helpers when a key does not exist or has expired.
var ErrCacheMiss = errors.New("bus: cache miss")

// cacheCASAttempts bounds the compare-and-swap loops of CacheAdd and CacheExpire under contention.
const cacheCASAttempts = 64

// CacheEntry is a value of the eb-cache bucket with its logical expiry (zero = never expires).
type CacheEntry struct {
	Value     []byte
	ExpiresAt time.Time
}

// The helpers below are the only way other packages use the eb-cache bucket (S4 §5, S10 §2.1): they take
// arbitrary string keys, keep a per-entry expiry and offer atomic counters, without exposing nats.go.
//
// Storage format. A NATS KV key allows only [-/_=.A-Za-z0-9], so every key is stored as the base64url of
// its SHA-256. NATS can set a per-message TTL only on a raw publish, and a plain KV update writes a new
// revision without one, so each value carries its own expiry: 8 bytes big-endian Unix milliseconds
// (0 = never) followed by the data. Reads enforce that expiry exactly; the message TTL (rounded up to
// whole seconds) only lets the server garbage-collect expired entries.

func cacheKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeCacheValue(value []byte, expiresAt time.Time) []byte {
	out := make([]byte, 8+len(value))
	var ms int64
	if !expiresAt.IsZero() {
		ms = expiresAt.UnixMilli()
	}
	binary.BigEndian.PutUint64(out, uint64(ms)) // #nosec G115 -- Unix milliseconds of a real time are positive
	copy(out[8:], value)
	return out
}

func decodeCacheValue(raw []byte) (CacheEntry, error) {
	if len(raw) < 8 {
		return CacheEntry{}, errors.New("bus: cache entry too short")
	}
	ms := int64(binary.BigEndian.Uint64(raw)) // #nosec G115 -- written by encodeCacheValue from an int64
	e := CacheEntry{Value: append([]byte(nil), raw[8:]...)}
	if ms != 0 {
		e.ExpiresAt = time.UnixMilli(ms)
	}
	return e, nil
}

func (e CacheEntry) expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt)
}

// msgTTL turns a logical expiry into the server-side message TTL (≥ 1 s, whole seconds); zero means none.
func msgTTL(expiresAt time.Time, now time.Time) time.Duration {
	if expiresAt.IsZero() {
		return 0
	}
	d := expiresAt.Sub(now)
	if d < time.Second {
		return time.Second
	}
	return d.Truncate(time.Second) + time.Second
}

func expiryFor(ttl time.Duration, now time.Time) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}

func (b *Bus) cacheSubject(k string) string { return "$KV." + BucketCache + "." + k }

// rawEntry reads the stored entry and its revision. found is false when the key does not exist.
func (b *Bus) rawEntry(ctx context.Context, k string) (CacheEntry, uint64, bool, error) {
	e, err := b.cache.Get(ctx, k)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return CacheEntry{}, 0, false, nil
	}
	if err != nil {
		return CacheEntry{}, 0, false, err
	}
	ce, err := decodeCacheValue(e.Value())
	if err != nil {
		return CacheEntry{}, 0, false, err
	}
	return ce, e.Revision(), true, nil
}

// CacheGet returns the entry stored under key, or ErrCacheMiss when it is missing or expired.
func (b *Bus) CacheGet(ctx context.Context, key string) (CacheEntry, error) {
	e, _, found, err := b.rawEntry(ctx, cacheKey(key))
	if err != nil {
		return CacheEntry{}, fmt.Errorf("bus: cache get: %w", err)
	}
	if !found || e.expired(time.Now()) {
		return CacheEntry{}, ErrCacheMiss
	}
	return e, nil
}

// CacheSet stores value under key; ttl ≤ 0 means the entry never expires.
func (b *Bus) CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	now := time.Now()
	exp := expiryFor(ttl, now)
	if err := b.publishCache(ctx, cacheKey(key), encodeCacheValue(value, exp), exp, now, nil); err != nil {
		return fmt.Errorf("bus: cache set: %w", err)
	}
	return nil
}

// CacheDelete removes key; deleting a missing key is not an error.
func (b *Bus) CacheDelete(ctx context.Context, key string) error {
	if err := b.cache.Delete(ctx, cacheKey(key)); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf("bus: cache delete: %w", err)
	}
	return nil
}

// CacheAdd atomically adds delta to the integer stored under key and returns the new value. A missing or
// expired key starts from 0 and gets ttlIfNew (≤ 0: never expires); an existing key keeps its expiry.
func (b *Bus) CacheAdd(ctx context.Context, key string, delta int64, ttlIfNew time.Duration) (int64, error) {
	k := cacheKey(key)
	for range cacheCASAttempts {
		now := time.Now()
		cur, rev, found, err := b.rawEntry(ctx, k)
		if err != nil {
			return 0, fmt.Errorf("bus: cache add: %w", err)
		}
		var n int64
		exp := cur.ExpiresAt
		if found && !cur.expired(now) {
			if n, err = strconv.ParseInt(string(cur.Value), 10, 64); err != nil {
				return 0, fmt.Errorf("bus: cache add: value of %q is not an integer", key)
			}
		} else {
			exp = expiryFor(ttlIfNew, now)
		}
		n += delta
		val := encodeCacheValue([]byte(strconv.FormatInt(n, 10)), exp)
		if !found {
			err = b.createCache(ctx, k, val, exp, now)
		} else {
			err = b.publishCache(ctx, k, val, exp, now, &rev)
		}
		if err == nil {
			return n, nil
		}
		if !isCASConflict(err) {
			return 0, fmt.Errorf("bus: cache add: %w", err)
		}
	}
	return 0, errors.New("bus: cache add: too much contention")
}

// CacheExpire sets a new ttl on an existing key (≤ 0: never expires); ErrCacheMiss when it is missing.
func (b *Bus) CacheExpire(ctx context.Context, key string, ttl time.Duration) error {
	k := cacheKey(key)
	for range cacheCASAttempts {
		now := time.Now()
		cur, rev, found, err := b.rawEntry(ctx, k)
		if err != nil {
			return fmt.Errorf("bus: cache expire: %w", err)
		}
		if !found || cur.expired(now) {
			return ErrCacheMiss
		}
		exp := expiryFor(ttl, now)
		err = b.publishCache(ctx, k, encodeCacheValue(cur.Value, exp), exp, now, &rev)
		if err == nil {
			return nil
		}
		if !isCASConflict(err) {
			return fmt.Errorf("bus: cache expire: %w", err)
		}
	}
	return errors.New("bus: cache expire: too much contention")
}

func (b *Bus) createCache(ctx context.Context, k string, val []byte, exp, now time.Time) error {
	var opts []jetstream.KVCreateOpt
	if ttl := msgTTL(exp, now); ttl > 0 {
		opts = append(opts, jetstream.KeyTTL(ttl))
	}
	_, err := b.cache.Create(ctx, k, val, opts...)
	return err
}

// publishCache writes a raw KV message with an optional message TTL and an optional expected revision.
func (b *Bus) publishCache(ctx context.Context, k string, val []byte, exp, now time.Time, rev *uint64) error {
	m := &nats.Msg{Subject: b.cacheSubject(k), Data: val, Header: nats.Header{}}
	var opts []jetstream.PublishOpt
	if ttl := msgTTL(exp, now); ttl > 0 {
		opts = append(opts, jetstream.WithMsgTTL(ttl))
	}
	if rev != nil {
		opts = append(opts, jetstream.WithExpectLastSequencePerSubject(*rev))
	}
	_, err := b.JS.PublishMsg(ctx, m, opts...)
	return err
}

func isCASConflict(err error) bool {
	if errors.Is(err, jetstream.ErrKeyExists) {
		return true
	}
	var apiErr *jetstream.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence || apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant)
}
