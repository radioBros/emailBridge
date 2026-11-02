package query

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

// TailFilter is the filter of a live tail (no time range, no cursor).
type TailFilter struct {
	Categories     []logging.Category
	MinLevel       string
	OrganizationID string
	AccountID      string
	Q              string
}

func (t TailFilter) filter() (store.Filter, error) {
	if err := checkCommon(t.Categories, t.MinLevel, t.Q); err != nil {
		return store.Filter{}, err
	}
	return store.Filter{Categories: t.Categories, Level: t.MinLevel, OrgID: t.OrganizationID, AccountID: t.AccountID, Q: t.Q}, nil
}

func isSummary(r *store.LogRecord) bool {
	v, _ := r.Attrs["summary"].(bool)
	return r.Component == "logging.tail" && v
}

// Tail delivers matching records from every node until stop is called. deliver must not block and must
// be safe for concurrent use: records arrive on the bus dispatcher while summaries come from a ticker
// goroutine (a non-blocking send on a buffered channel satisfies both). The stream is capped at TailRate
// records per second, the excess summarised once per second as an "N more" record. Remote tails are
// refreshed every store.RemoteTailTTL/3 and expire on their own if this node disappears.
func (s *Service) Tail(ctx context.Context, tf TailFilter, deliver func(store.LogRecord)) (stop func(), err error) {
	f, err := tf.filter()
	if err != nil {
		return nil, err
	}
	if s.o.Bus == nil {
		if s.o.Local == nil || s.o.Local.Hub == nil {
			return nil, errors.New("logquery: no tail hub")
		}
		return s.o.Local.Hub.Subscribe(f, deliver), nil
	}
	var id [12]byte
	if _, err := crand.Read(id[:]); err != nil {
		return nil, err
	}
	tailID := "t" + hex.EncodeToString(id[:])
	lim := &store.Limiter{Rate: s.o.TailRate}
	unsub, err := s.o.Bus.SubscribeCore(store.TailSubject(tailID), func(_ string, data []byte) {
		var r store.LogRecord
		if json.Unmarshal(data, &r) != nil {
			return
		}
		if isSummary(&r) {
			if n, ok := r.Attrs["more"].(float64); ok {
				lim.Drop(int64(n)) // fold node summaries into ours so the client sees one count
			}
			return
		}
		if lim.Admit(time.Now()) {
			deliver(r)
		}
	})
	if err != nil {
		return nil, err
	}
	register := func(rctx context.Context) {
		if _, _, err := s.scatter(rctx, store.Request{Op: store.OpTail, TailID: tailID, Filter: &f}); err != nil {
			s.o.Log.LogAttrs(rctx, slog.LevelWarn, "log tail registration failed", slog.Any("err", err))
		}
	}
	register(ctx)
	// the refresh loop outlives the request that started the tail; stop ends it
	loopCtx, cancelLoop := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		refresh := time.NewTicker(store.RemoteTailTTL / 3)
		summary := time.NewTicker(time.Second)
		defer refresh.Stop()
		defer summary.Stop()
		for {
			select {
			case <-done:
				return
			case <-refresh.C:
				register(loopCtx)
			case <-summary.C:
				if n := lim.TakeDropped(); n > 0 {
					deliver(store.SummaryRecord(s.o.Node, n, time.Now()))
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			cancelLoop()
			wg.Wait()
			uctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, _ = s.scatter(uctx, store.Request{Op: store.OpUntail, TailID: tailID})
			_ = unsub()
		})
	}, nil
}
