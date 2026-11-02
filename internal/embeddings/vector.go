package embeddings

import (
	"encoding/binary"
	"math"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// unit returns v scaled to length 1 (ok false for a zero or non-finite vector). Stored and query vectors are
// unit length, so their dot product is the cosine similarity.
func unit(v []float32) ([]float32, bool) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil, false
	}
	inv := 1 / math.Sqrt(sum)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) * inv)
	}
	return out, true
}

// dotPacked is the dot product of q with a stored vector: the data of a BSON binary vector (subtype 9) of
// float32 — one dtype byte (0x27), one padding byte, then little-endian float32s. ok is false when the data
// is not a float32 vector of len(q) dimensions.
func dotPacked(q []float32, data []byte) (float64, bool) {
	if len(data) != 2+4*len(q) || data[0] != bson.Float32Vector || data[1] != 0 {
		return 0, false
	}
	var sum float64
	p := data[2:]
	for i, x := range q {
		sum += float64(x) * float64(math.Float32frombits(binary.LittleEndian.Uint32(p[4*i:])))
	}
	return sum, true
}

// bucket is a token bucket.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a set of token buckets keyed by organisation or user, per node.
type limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu sync.Mutex
	m  map[string]*bucket
}

func newLimiter(perMinute, burst float64) *limiter {
	return &limiter{rate: perMinute / 60, burst: max(burst, 1), m: map[string]*bucket{}}
}

func (l *limiter) refill(key string, now time.Time) *bucket {
	b := l.m[key]
	if b == nil {
		if len(l.m) > 10000 { // bounded: forget everyone rather than track ages
			l.m = map[string]*bucket{}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(l.burst, b.tokens+el*l.rate)
		b.last = now
	}
	return b
}

// allow takes a token when one is there; otherwise it reports how long until one is.
func (l *limiter) allow(key string, now time.Time) (bool, time.Duration) {
	if l.rate <= 0 { // no limit
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key, now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// reserve takes a token now or in the future and returns how long to wait for it (background work queues
// up behind the limit instead of failing).
func (l *limiter) reserve(key string, now time.Time) time.Duration {
	if l.rate <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key, now)
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / l.rate * float64(time.Second))
}
