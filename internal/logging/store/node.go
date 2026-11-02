package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
)

// Operations of the eb.logs.query envelope (S12 §8, S11 §5.1).
const (
	OpQuery  = "query"  // Filter → Records
	OpGet    = "get"    // ID → Record
	OpStats  = "stats"  // Filter (range, org) → Stats
	OpErase  = "erase"  // AccountID → Erased
	OpTail   = "tail"   // TailID + Filter: start or refresh a remote tail publishing to TailSubject(TailID)
	OpUntail = "untail" // TailID: stop a remote tail
)

// Reply error codes (details stay in the answering node's log).
const (
	ReplyBadRequest = "bad_request"
	ReplyBadFilter  = "bad_filter"
	ReplyFailed     = "failed"
)

// Request is the eb.logs.query envelope.
type Request struct {
	Op        string  `json:"op"`
	Filter    *Filter `json:"filter,omitempty"`
	ID        string  `json:"id,omitempty"`
	AccountID string  `json:"accountId,omitempty"`
	TailID    string  `json:"tailId,omitempty"`
	// TimeoutMs is the requester's remaining deadline; the node stops working after it.
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
}

// Reply is one node's answer. Records are pre-encoded LogRecords so the node can fit them into one
// NATS message; Truncated means fewer records than asked were returned for that reason, and the
// requester must not page past the last one returned.
type Reply struct {
	Node      string            `json:"node"`
	Records   []json.RawMessage `json:"records,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Record    *LogRecord        `json:"record,omitempty"`
	Stats     *Stats            `json:"stats,omitempty"`
	Erased    int64             `json:"erased,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// TailSubject is the core subject a remote tail publishes its records on.
func TailSubject(tailID string) string { return "eb.logs.tail." + tailID }

// tailIDRe keeps tail IDs one subject token.
var tailIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidTailID reports whether id can name a tail subscription.
func ValidTailID(id string) bool { return tailIDRe.MatchString(id) }

// RemoteTailTTL is how long a remote tail lives without a refresh; requesters refresh every
// RemoteTailTTL/3.
const RemoteTailTTL = 30 * time.Second

// Publisher publishes core messages (implemented by *bus.Bus).
type Publisher interface {
	PublishCore(subject string, data []byte) error
}

// Node answers eb.logs.query for this node from its Store and Hub.
type Node struct {
	Store *Store
	Hub   *Hub
	// Pub publishes remote-tail records; nil disables remote tails.
	Pub Publisher
	// Log receives failures of the node's side (system category).
	Log *slog.Logger
	// MaxPayload bounds an encoded reply (default 1 MiB).
	MaxPayload int64
	// Now is the clock (tests).
	Now func() time.Time

	mu    sync.Mutex
	tails map[string]*remoteTail
}

type remoteTail struct {
	cancel  func()
	expires time.Time
}

func (n *Node) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *Node) log() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Serve answers eb.logs.query on b until the returned stop function is called. It also expires remote
// tails that are not refreshed.
func (n *Node) Serve(b *bus.Bus) (stop func(), err error) {
	if n.MaxPayload <= 0 {
		n.MaxPayload = b.MaxPayload()
	}
	unsub, err := b.Respond(bus.LogsQuerySubject, n.Handle)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(RemoteTailTTL / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				n.ExpireTails()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			unsub()
			close(done)
			wg.Wait()
			n.stopTails()
		})
	}, nil
}

// Handle executes one encoded Request and returns the encoded Reply.
func (n *Node) Handle(ctx context.Context, data []byte) []byte {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return n.encode(&Reply{Error: ReplyBadRequest})
	}
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	rep := Reply{}
	var err error
	switch req.Op {
	case OpQuery:
		if req.Filter == nil {
			rep.Error = ReplyBadRequest
			break
		}
		var recs []LogRecord
		recs, err = n.Store.Search(ctx, *req.Filter)
		if err == nil {
			return n.encodeRecords(recs)
		}
	case OpGet:
		rep.Record, err = n.Store.Get(ctx, req.ID)
	case OpStats:
		if req.Filter == nil {
			rep.Error = ReplyBadRequest
			break
		}
		rep.Stats, err = n.Store.Stats(ctx, *req.Filter)
	case OpErase:
		rep.Erased, err = n.Store.Erase(ctx, req.AccountID)
	case OpTail:
		if req.Filter == nil || !ValidTailID(req.TailID) || n.Pub == nil || n.Hub == nil {
			rep.Error = ReplyBadRequest
			break
		}
		n.startTail(req.TailID, *req.Filter)
	case OpUntail:
		n.stopTail(req.TailID)
	default:
		rep.Error = ReplyBadRequest
	}
	if err != nil {
		rep = Reply{Error: ReplyFailed}
		if errors.Is(err, ErrBadFilter) {
			rep.Error = ReplyBadFilter
		} else {
			n.log().LogAttrs(ctx, slog.LevelWarn, "log query failed on node", slog.String("op", req.Op), slog.Any("err", err))
		}
	}
	return n.encode(&rep)
}

func (n *Node) encode(rep *Reply) []byte {
	rep.Node = n.Store.Node()
	b, err := json.Marshal(rep)
	if err != nil {
		b, _ = json.Marshal(&Reply{Node: n.Store.Node(), Error: ReplyFailed})
	}
	return b
}

// replyOverhead leaves room for the envelope and NATS headers.
const replyOverhead = 4 << 10

// encodeRecords fits as many records (newest first) as the payload limit allows. A single record that
// is too large on its own loses its attributes and stack so paging always progresses.
func (n *Node) encodeRecords(recs []LogRecord) []byte {
	budget := n.MaxPayload
	if budget <= 0 {
		budget = 1 << 20
	}
	budget -= replyOverhead
	rep := Reply{Records: make([]json.RawMessage, 0, len(recs))}
	var used int64
	for i := range recs {
		b, err := json.Marshal(&recs[i])
		if err != nil {
			continue
		}
		if used+int64(len(b))+1 > budget {
			if len(rep.Records) == 0 {
				slim := recs[i]
				slim.Attrs = map[string]any{"_truncated": true}
				slim.Stack = nil
				if b, err = json.Marshal(&slim); err == nil && int64(len(b)) <= budget {
					rep.Records = append(rep.Records, b)
				}
			}
			rep.Truncated = true
			break
		}
		used += int64(len(b)) + 1
		rep.Records = append(rep.Records, b)
	}
	return n.encode(&rep)
}

func (n *Node) startTail(id string, f Filter) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tails == nil {
		n.tails = map[string]*remoteTail{}
	}
	if t, ok := n.tails[id]; ok {
		t.expires = n.now().Add(RemoteTailTTL)
		return
	}
	subject := TailSubject(id)
	cancel := n.Hub.Subscribe(f, func(r LogRecord) {
		if b, err := json.Marshal(&r); err == nil {
			_ = n.Pub.PublishCore(subject, b)
		}
	})
	n.tails[id] = &remoteTail{cancel: cancel, expires: n.now().Add(RemoteTailTTL)}
}

func (n *Node) stopTail(id string) {
	n.mu.Lock()
	t, ok := n.tails[id]
	delete(n.tails, id)
	n.mu.Unlock()
	if ok {
		t.cancel()
	}
}

// ExpireTails stops remote tails whose requester stopped refreshing them.
func (n *Node) ExpireTails() {
	now := n.now()
	n.mu.Lock()
	var expired []*remoteTail
	for id, t := range n.tails {
		if now.After(t.expires) {
			expired = append(expired, t)
			delete(n.tails, id)
		}
	}
	n.mu.Unlock()
	for _, t := range expired {
		t.cancel()
	}
}

// Tails returns the number of active remote tails.
func (n *Node) Tails() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.tails)
}

func (n *Node) stopTails() {
	n.mu.Lock()
	tails := n.tails
	n.tails = nil
	n.mu.Unlock()
	for _, t := range tails {
		t.cancel()
	}
}
