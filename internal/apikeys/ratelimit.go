package apikeys

import (
	"context"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// Per-key in-process token buckets (rps refill, burst capacity). Local to each API node.
type keyBucket struct {
	tokens float64
	last   time.Time
}

var (
	keyRateMu sync.Mutex
	keyRates  = map[string]*keyBucket{}
)

func takeKeyRate(ctx context.Context, keyID string, rl *RateLimit) error {
	if rl == nil || rl.RPS <= 0 {
		return nil
	}
	burst := float64(rl.Burst)
	if burst < 1 {
		burst = 1
	}
	now := time.Now().UTC()
	keyRateMu.Lock()
	defer keyRateMu.Unlock()
	b := keyRates[keyID]
	if b == nil {
		b = &keyBucket{tokens: burst, last: now}
		keyRates[keyID] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.RPS
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	// TokenBucketInfo expects rate-per-minute; convert rps → per minute for Reset math.
	ratePerMin := rl.RPS * 60
	if b.tokens < 1 {
		info := errs.TokenBucketInfo(burst, ratePerMin, b.tokens)
		httpapi.NoteRateLimit(ctx, info)
		return errs.New(errs.CodeRateLimited, "API key rate limit exceeded.").WithRate(info)
	}
	b.tokens--
	httpapi.NoteRateLimit(ctx, errs.TokenBucketInfo(burst, ratePerMin, b.tokens))
	return nil
}
