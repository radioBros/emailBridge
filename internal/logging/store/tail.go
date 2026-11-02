package store

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/radiobros/emailbridge/internal/logging"
)

// TailRate is the default live-tail cap per subscriber (S12 §8): records beyond it in one second are
// summarised as one "N more" record.
const TailRate = 50

// TailSinkName is the logging.Sink name of the tail hub.
const TailSinkName = "logtail"

// Limiter caps a subscriber to Rate records per one-second window and counts the excess.
type Limiter struct {
	Rate int

	mu      sync.Mutex
	window  time.Time
	n       int
	dropped int64
}

// Admit reports whether one more record may be delivered at now.
func (l *Limiter) Admit(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Second {
		l.window, l.n = now, 0
	}
	if l.n < l.Rate {
		l.n++
		return true
	}
	l.dropped++
	return false
}

// Drop counts a record lost for another reason (a full buffer) so it appears in the next summary.
func (l *Limiter) Drop(n int64) {
	l.mu.Lock()
	l.dropped += n
	l.mu.Unlock()
}

// TakeDropped returns and resets the count of records not delivered since the last call.
func (l *Limiter) TakeDropped() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.dropped
	l.dropped = 0
	return n
}

// SummaryRecord is the synthetic record that stands for n records a subscriber did not receive. It is
// never stored.
func SummaryRecord(node string, n int64, now time.Time) LogRecord {
	id := ulid.MustNew(ulid.Timestamp(now), crand.Reader)
	return LogRecord{
		ID: "log_" + strings.ToLower(id.String()), Time: now.UTC().UnixNano(), Level: "info",
		Category: string(logging.CatSystem), Component: "logging.tail", Node: node,
		Msg: fmt.Sprintf("%d more", n), Attrs: map[string]any{"more": n, "summary": true},
	}
}

// Hub is the live-tail tee of the node: a logging.Sink that hands matching records to subscribers
// without ever blocking the log writer (each subscriber has a bounded buffer and a rate cap).
type Hub struct {
	node string
	rate int
	now  func() time.Time

	mu   sync.RWMutex
	subs map[*subscription]struct{}

	stop    chan struct{}
	stopped sync.Once
	wg      sync.WaitGroup
}

type subscription struct {
	f       Filter
	ch      chan LogRecord
	lim     *Limiter
	deliver func(LogRecord)
	done    chan struct{}
	once    sync.Once
}

// NewHub starts a hub. rate <= 0 uses TailRate.
func NewHub(node string, rate int) *Hub {
	if rate <= 0 {
		rate = TailRate
	}
	h := &Hub{node: node, rate: rate, now: time.Now, subs: map[*subscription]struct{}{}, stop: make(chan struct{})}
	h.wg.Add(1)
	go h.summaries()
	return h
}

// Name implements logging.Sink.
func (h *Hub) Name() string { return TailSinkName }

// Write implements logging.Sink.
func (h *Hub) Write(_ context.Context, batch []logging.Record) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.subs) == 0 {
		return nil
	}
	now := h.now()
	for i := range batch {
		r := FromRecord(&batch[i])
		for sub := range h.subs {
			if !sub.f.Matches(&r) {
				continue
			}
			if !sub.lim.Admit(now) {
				continue
			}
			select {
			case sub.ch <- r:
			default:
				sub.lim.Drop(1)
			}
		}
	}
	return nil
}

// Subscribe delivers records matching f to deliver (called from one goroutine per subscription, in
// order) until cancel is called. f's time range, cursor and limit are ignored.
func (h *Hub) Subscribe(f Filter, deliver func(LogRecord)) (cancel func()) {
	_, _ = f.minLevel() // warm the caches before concurrent use
	f.Matches(&LogRecord{})
	sub := &subscription{f: f, ch: make(chan LogRecord, 256), lim: &Limiter{Rate: h.rate}, deliver: deliver, done: make(chan struct{})}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case r := <-sub.ch:
				sub.deliver(r)
			case <-sub.done:
				return
			}
		}
	}()
	return func() {
		sub.once.Do(func() {
			h.mu.Lock()
			delete(h.subs, sub)
			h.mu.Unlock()
			close(sub.done)
		})
	}
}

// Subscribers returns the number of active subscriptions.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// summaries emits one "N more" record per second for every subscriber that lost records.
func (h *Hub) summaries() {
	defer h.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.flushSummaries()
		}
	}
}

func (h *Hub) flushSummaries() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subs {
		if n := sub.lim.TakeDropped(); n > 0 {
			select {
			case sub.ch <- SummaryRecord(h.node, n, h.now()):
			default:
				sub.lim.Drop(n) // retry next second
			}
		}
	}
}

// Close cancels every subscription and stops the hub.
func (h *Hub) Close() {
	h.stopped.Do(func() {
		h.mu.Lock()
		for sub := range h.subs {
			sub.once.Do(func() { close(sub.done) })
			delete(h.subs, sub)
		}
		h.mu.Unlock()
		close(h.stop)
		h.wg.Wait()
	})
}
