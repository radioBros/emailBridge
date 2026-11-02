package httpapi

import (
	"context"
	"net/http"
	"sync"

	"github.com/radiobros/emailbridge/internal/errs"
)

// RateLimit-* headers (S2 §2). An operation guarded by an in-process limiter (account verify, autoconfig,
// live health checks, send quotas) calls NoteRateLimit with the limiter state; the response — success or
// problem — then carries RateLimit-Limit / RateLimit-Remaining / RateLimit-Reset. A 429 problem also carries
// Retry-After (errs.Problem.GetHeaders). Operations without such a limiter send no RateLimit-* headers.

type rateNoteKey struct{}

type rateNote struct {
	mu   sync.Mutex
	info *errs.RateInfo
}

func withRateNote(ctx context.Context) (context.Context, *rateNote) {
	n := &rateNote{}
	return context.WithValue(ctx, rateNoteKey{}, n), n
}

// NoteRateLimit records the state of the limiter that answered this request; a no-op outside a request.
func NoteRateLimit(ctx context.Context, info errs.RateInfo) {
	n, ok := ctx.Value(rateNoteKey{}).(*rateNote)
	if !ok || n == nil {
		return
	}
	n.mu.Lock()
	n.info = &info
	n.mu.Unlock()
}

// apply writes the noted state on h unless a handler already set RateLimit-* itself.
func (n *rateNote) apply(h http.Header) {
	if n == nil {
		return
	}
	n.mu.Lock()
	info := n.info
	n.mu.Unlock()
	if info == nil || h.Get("RateLimit-Limit") != "" {
		return
	}
	errs.SetRateHeaders(h, *info)
}
