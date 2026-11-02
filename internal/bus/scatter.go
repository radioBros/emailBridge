package bus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Core (non-JetStream) scatter-gather used by node-local services such as the log store (S4 §2, S12 §8):
// a request answered by every node, replies collected until a deadline. Plain publish/subscribe lives in
// core.go. These helpers keep nats.go inside this package (S0 §3.4).

// ErrNotConnected is returned when the bus has no usable connection.
var ErrNotConnected = errors.New("bus: not connected")

// respondConcurrency bounds the requests one Respond subscription handles at the same time, so a slow
// request (a 31-day log query) never blocks the others while a flood cannot spawn unbounded goroutines.
const respondConcurrency = 8

// noRespondersStatus is the status header value of the server's "no responders" notice.
const noRespondersStatus = "503"

// ScatterGather publishes req on subject with a unique reply inbox and collects replies until expect
// replies have arrived, the deadline passes or ctx ends, whichever comes first. expect <= 0 waits for
// the full deadline. The replies are returned in arrival order; reaching the deadline is not an error.
func (b *Bus) ScatterGather(ctx context.Context, subject string, req []byte, expect int, deadline time.Duration) ([][]byte, error) {
	if b == nil || b.Conn == nil || b.Conn.IsClosed() {
		return nil, ErrNotConnected
	}
	if deadline <= 0 {
		return nil, errors.New("bus: scatter-gather needs a positive deadline")
	}
	inbox := b.Conn.NewRespInbox()
	ch := make(chan *nats.Msg, 64)
	sub, err := b.Conn.ChanSubscribe(inbox, ch)
	if err != nil {
		return nil, fmt.Errorf("bus: scatter-gather subscribe: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := b.Conn.PublishRequest(subject, inbox, req); err != nil {
		return nil, fmt.Errorf("bus: scatter-gather publish %s: %w", subject, err)
	}
	if err := b.Conn.Flush(); err != nil {
		return nil, fmt.Errorf("bus: scatter-gather flush: %w", err)
	}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	var out [][]byte
	for expect <= 0 || len(out) < expect {
		select {
		case m := <-ch:
			if len(m.Data) == 0 && m.Header.Get("Status") == noRespondersStatus {
				return out, nil // the server says nobody subscribes to subject
			}
			out = append(out, m.Data)
		case <-timer.C:
			return out, nil
		case <-ctx.Done():
			return out, nil
		}
	}
	return out, nil
}

// Respond subscribes h to subject on every node (no queue group: each node answers a scatter-gather
// request). h runs in its own goroutine, at most respondConcurrency at a time; its ctx is cancelled when
// the subscription is removed. A nil reply sends nothing. The returned function unsubscribes and waits for
// running handlers to finish.
func (b *Bus) Respond(subject string, h func(ctx context.Context, req []byte) []byte) (unsubscribe func(), err error) {
	if b == nil || b.Conn == nil || b.Conn.IsClosed() {
		return nil, ErrNotConnected
	}
	ctx, cancel := context.WithCancel(context.Background())
	sem := make(chan struct{}, respondConcurrency)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		closed bool
	)
	sub, err := b.Conn.Subscribe(subject, func(m *nats.Msg) {
		if m.Reply == "" {
			return
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			<-sem
			return
		}
		wg.Add(1)
		mu.Unlock()
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if reply := h(ctx, m.Data); reply != nil {
				_ = m.Respond(reply)
			}
		}()
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bus: respond subscribe %s: %w", subject, err)
	}
	if err := b.Conn.Flush(); err != nil {
		_ = sub.Unsubscribe()
		cancel()
		return nil, fmt.Errorf("bus: respond flush: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = sub.Unsubscribe()
			mu.Lock()
			closed = true
			mu.Unlock()
			cancel()
			wg.Wait()
		})
	}, nil
}

// MaxPayload is the largest message the server accepts (1 MiB by default). Repliers size their answers
// to fit.
func (b *Bus) MaxPayload() int64 {
	if b == nil || b.Conn == nil {
		return 1 << 20
	}
	return b.Conn.MaxPayload()
}

// NodeIDs lists the node IDs currently registered in the eb-nodes bucket (S4 §5), sorted. An empty
// registry returns an empty list, not an error.
func (b *Bus) NodeIDs(ctx context.Context) ([]string, error) {
	if b == nil || b.nodes == nil {
		return nil, ErrNotConnected
	}
	lister, err := b.nodes.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("bus: list nodes: %w", err)
	}
	defer func() { _ = lister.Stop() }()
	var ids []string
	for k := range lister.Keys() {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids, nil
}

// Embedded reports whether this bus runs the in-process NATS server; an embedded bus is always a
// single-node deployment (S4 §1), so scatter-gather callers know every responder is this node.
func (b *Bus) Embedded() bool { return b != nil && b.srv != nil }
