package errs

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

// Rate-limit response headers (S2 §2): every 429 carries Retry-After; responses of operations guarded by an
// in-process limiter also carry RateLimit-Limit / RateLimit-Remaining / RateLimit-Reset.

// DefaultRetryAfter is the Retry-After of a 429 whose limiter gave no better hint.
const DefaultRetryAfter = 60 * time.Second

// RateInfo is the state of the limiter that answered a request.
type RateInfo struct {
	Limit     int           // requests allowed per window
	Remaining int           // requests left in the current window
	Reset     time.Duration // until the window (or the next token) frees capacity
}

// WithRetryAfter sets the Retry-After hint sent when e becomes a 429 (whole seconds, at least 1).
func (e *E) WithRetryAfter(d time.Duration) *E {
	e.retryAfter = d
	return e
}

// WithoutRetryAfter marks a 429 where waiting does not help (an account-count quota): no Retry-After.
func (e *E) WithoutRetryAfter() *E {
	e.noRetryAfter = true
	return e
}

// WithRate attaches the limiter state, sent as RateLimit-* headers with the problem.
func (e *E) WithRate(r RateInfo) *E {
	e.rate = &r
	return e
}

// Seconds formats d as a header value in whole seconds, rounded up, at least 1.
func Seconds(d time.Duration) string {
	s := int64(math.Ceil(d.Seconds()))
	if s < 1 {
		s = 1
	}
	return strconv.FormatInt(s, 10)
}

// SetRateHeaders writes the RateLimit-* headers of r on h.
func SetRateHeaders(h http.Header, r RateInfo) {
	rem := r.Remaining
	if rem < 0 {
		rem = 0
	}
	reset := int64(math.Ceil(r.Reset.Seconds()))
	if reset < 0 {
		reset = 0
	}
	h.Set("RateLimit-Limit", strconv.Itoa(r.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(rem))
	h.Set("RateLimit-Reset", strconv.FormatInt(reset, 10))
}

// problemHeaders builds the response headers of a problem with status from e (nil e: a plain problem).
func problemHeaders(status int, e *E) http.Header {
	if status != http.StatusTooManyRequests && (e == nil || e.rate == nil) {
		return nil
	}
	h := http.Header{}
	if e != nil && e.rate != nil {
		SetRateHeaders(h, *e.rate)
	}
	if status == http.StatusTooManyRequests && (e == nil || !e.noRetryAfter) {
		wait := DefaultRetryAfter
		switch {
		case e != nil && e.retryAfter > 0:
			wait = e.retryAfter
		case e != nil && e.rate != nil && e.rate.Reset > 0:
			wait = e.rate.Reset
		}
		h.Set("Retry-After", Seconds(wait))
	}
	return h
}

// GetHeaders implements huma.HeadersError: Retry-After on a 429, RateLimit-* when the limiter is known.
func (p *Problem) GetHeaders() http.Header {
	if p == nil {
		return nil
	}
	if p.headers == nil && p.Status == http.StatusTooManyRequests {
		return problemHeaders(p.Status, nil)
	}
	return p.headers
}

// TokenBucketInfo is the RateInfo of a token bucket of capacity refilled at ratePerMinute with tokens left:
// Reset is the wait for the next token when empty, otherwise the time until the bucket is full again.
func TokenBucketInfo(capacity, ratePerMinute, tokens float64) RateInfo {
	info := RateInfo{Limit: int(capacity), Remaining: int(math.Floor(tokens))}
	if ratePerMinute <= 0 {
		return info
	}
	need := capacity - tokens
	if tokens < 1 {
		need = 1 - tokens
	}
	if need > 0 {
		info.Reset = time.Duration(need / ratePerMinute * float64(time.Minute))
	}
	return info
}
