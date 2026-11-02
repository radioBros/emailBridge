package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// StreamSubscriber is the part of *bus.Bus the SSE fan-in uses.
type StreamSubscriber interface {
	SubscribeStream(ctx context.Context, stream, filter string, all bool, h func(subject string, data []byte)) (func(), error)
}

// MaxStreamsPerOrg is the documented per-organisation concurrent SSE cap per node (S7 §7).
const MaxStreamsPerOrg = 20

type streamLimiter struct {
	mu  sync.Mutex
	max int
	n   map[string]int
}

func newStreamLimiter(max int) *streamLimiter {
	if max <= 0 {
		max = MaxStreamsPerOrg
	}
	return &streamLimiter{max: max, n: map[string]int{}}
}

func (l *streamLimiter) acquire(org string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[org] >= l.max {
		return false
	}
	l.n[org]++
	return true
}

func (l *streamLimiter) release(org string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[org] <= 1 {
		delete(l.n, org)
		return
	}
	l.n[org]--
}

func (a *api) stream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := tenancy.RequireRead(ctx, "events"); err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeInternalError, "Organisation context is missing."))
		return
	}
	if !a.sseEnabled(ctx, org) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeFeatureDisabled, "Event streaming is disabled for this organisation."))
		return
	}
	if a.o.Subscriber == nil {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeServiceUnavailable, "Event streaming is not available on this node."))
		return
	}
	if !a.lim.acquire(org) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeRateLimited, "Too many concurrent event streams for this organisation on this node."))
		return
	}
	defer a.lim.release(org)

	qv := r.URL.Query()
	types := normalizeTypes(qv["type"])
	accountID := strings.TrimSpace(qv.Get("accountId"))
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if accountID != "" && accountID != c.AccountID {
			httpapi.WriteProblem(w, r, errs.New(errs.CodeForbidden, "The API key is bound to a different account."))
			return
		}
		accountID = c.AccountID
	}
	if accountID != "" && !store.IsID(store.PrefixAccount, accountID) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeValidationFailed, "accountId is not a valid account id."))
		return
	}
	if _, err := typeFilter(types); err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}

	lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if lastID == "" {
		lastID = strings.TrimSpace(qv.Get("lastEventId"))
	}
	var resumeFrom time.Time
	if lastID != "" {
		if !store.IsID(store.PrefixEvent, lastID) {
			httpapi.WriteProblem(w, r, errs.New(errs.CodeValidationFailed, "Last-Event-ID is not a valid event id."))
			return
		}
		anchor, err := a.w.Get(ctx, lastID)
		if err != nil {
			if errs.CodeOf(err) == errs.CodeNotFound {
				httpapi.WriteProblem(w, r, errs.New(errs.CodeReplayWindowExceeded, "Last-Event-ID is outside the retention window."))
				return
			}
			httpapi.WriteProblem(w, r, err)
			return
		}
		if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" && anchor.EBAccount != c.AccountID {
			httpapi.WriteProblem(w, r, errs.New(errs.CodeReplayWindowExceeded, "Last-Event-ID is outside the retention window."))
			return
		}
		resumeFrom = anchor.Time.Add(-resumeSkew)
	}

	live := make(chan Event, a.o.StreamBuffer)
	overflow := make(chan struct{}, 1)
	// Filter on the server: eb.evt.<p>.<org>.<type…> (S4 §2), so a stream only receives its organisation.
	stop, err := a.o.Subscriber.SubscribeStream(ctx, bus.StreamEvents, "eb.evt.*."+org+".>", false, func(_ string, data []byte) {
		var e Event
		if json.Unmarshal(data, &e) != nil || e.EBOrg != org || e.ID == "" {
			return
		}
		if accountID != "" && e.EBAccount != accountID {
			return
		}
		if !matchType(e.Type, types) {
			return
		}
		select {
		case live <- e:
		default:
			select {
			case overflow <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		httpapi.WriteProblem(w, r, errs.Wrap(err, errs.CodeServiceUnavailable, "events.stream.subscribe"))
		return
	}
	defer stop()

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, ": stream started\n\n"); err != nil {
		return
	}
	_ = rc.Flush()

	emitted := map[string]struct{}{}
	writeEvent := func(e Event) error {
		b, err := json.Marshal(&e)
		if err != nil {
			return nil
		}
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b); err != nil {
			return err
		}
		return rc.Flush()
	}

	if !resumeFrom.IsZero() {
		after := ""
		for {
			batch, err := a.w.CatchUp(ctx, resumeFrom, after, types, accountID, catchUpBatch)
			if err != nil {
				return
			}
			for _, e := range batch {
				emitted[e.ID] = struct{}{}
				if err := writeEvent(e); err != nil {
					return
				}
				after = e.ID
			}
			if len(batch) < catchUpBatch {
				break
			}
			if len(emitted) > catchUpBatch*4 {
				emitted = trimEmitted(emitted, catchUpBatch*2)
			}
		}
	}

	keep := time.NewTicker(a.o.KeepAlive)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-overflow:
			return // slow client; they resume with Last-Event-ID
		case <-keep.C:
			if _, err := io.WriteString(w, ":keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case e, ok := <-live:
			if !ok {
				return
			}
			if _, seen := emitted[e.ID]; seen {
				continue
			}
			if err := writeEvent(e); err != nil {
				return
			}
		}
	}
}

func matchType(typ string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if strings.HasSuffix(f, ".*") {
			if strings.HasPrefix(typ, strings.TrimSuffix(f, "*")) {
				return true
			}
			continue
		}
		if typ == f {
			return true
		}
	}
	return false
}

func trimEmitted(m map[string]struct{}, keep int) map[string]struct{} {
	if len(m) <= keep {
		return m
	}
	// Keep the newest IDs (ULIDs sort by time): only recent events can overlap with the live subscription.
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make(map[string]struct{}, keep)
	for _, id := range ids[len(ids)-keep:] {
		out[id] = struct{}{}
	}
	return out
}
