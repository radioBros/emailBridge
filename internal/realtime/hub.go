// Package realtime is the admin WebSocket hub (S13 §6, S10 §9): one socket per browser tab, topic
// subscriptions checked against the caller's permissions, and messages fanned in from NATS so any api
// node can serve any socket in cluster mode.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Limits (S10 §9).
const (
	MaxMessageBytes   = 64 << 10
	MaxSubscriptions  = 50
	ClientIdleTimeout = 60 * time.Second
)

// Topic declares a subscribable topic. Params validates the client's subscription parameters and returns
// a filter deciding, per published message, whether this subscription receives it.
type Topic struct {
	Name       string
	Permission tenancy.Permission
	// ReadScope, for a PermRead topic, is the S10 §3 resource scope it reads: API keys need `<scope>:read`.
	ReadScope string
	Params    func(params json.RawMessage) (Filter, error)
	// Visible, when set, decides per message whether this subscriber may see it — for topics whose messages
	// are not all visible to every member of the organisation (e.g. instance-scope notifications, W75).
	Visible func(c tenancy.Caller, msg Message) bool
}

// allows reports whether caller may subscribe to t.
func (t Topic) allows(c tenancy.Caller) bool {
	if t.Permission == tenancy.PermRead {
		return t.ReadScope != "" && c.AllowsRead(t.ReadScope)
	}
	return c.Allows(t.Permission)
}

// Filter decides whether a message (its params) goes to one subscription.
type Filter func(msg Message) bool

// Message is what publishers send and sockets receive (S13 §6).
type Message struct {
	Topic string          `json:"topic"`
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	// Org is the organisation the message belongs to (never sent to clients).
	Org string `json:"org"`
	// Keys carries routing attributes for filters (e.g. accountId); not sent to clients.
	Keys map[string]string `json:"keys,omitempty"`
}

// Core is the part of *bus.Bus the hub uses (fakeable in tests).
type Core interface {
	PublishCore(subject string, data []byte) error
	SubscribeCore(subject string, h func(subject string, data []byte)) (func() error, error)
}

// Hub holds the topic registry and the sockets of this node.
type Hub struct {
	core    Core
	log     *slog.Logger
	origins []string // host patterns for the Origin check

	mu      sync.RWMutex
	topics  map[string]Topic
	sockets map[*socket]struct{}
	unsub   func() error
	seq     atomic.Uint64
}

// NewHub creates a hub. trustedOrigins are full origins (https://host[:port]); their hosts are the
// accepted Origin patterns.
func NewHub(core Core, log *slog.Logger, trustedOrigins []string) *Hub {
	if log == nil {
		log = slog.Default()
	}
	h := &Hub{core: core, log: log, topics: map[string]Topic{}, sockets: map[*socket]struct{}{}}
	for _, o := range trustedOrigins {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			h.origins = append(h.origins, u.Host)
		}
	}
	return h
}

// Register adds a topic; registering the same name twice is an error.
func (h *Hub) Register(t Topic) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t.Name == "" || t.Permission == "" {
		return errors.New("realtime: topic needs a name and a permission")
	}
	if t.Permission == tenancy.PermRead && t.ReadScope == "" {
		return fmt.Errorf("realtime: read topic %q needs a read scope", t.Name)
	}
	if _, dup := h.topics[t.Name]; dup {
		return fmt.Errorf("realtime: topic %q registered twice", t.Name)
	}
	h.topics[t.Name] = t
	return nil
}

// Start subscribes to the realtime subjects of every organisation on this node.
func (h *Hub) Start() error {
	unsub, err := h.core.SubscribeCore("eb.rt.>", func(_ string, data []byte) {
		var m Message
		if json.Unmarshal(data, &m) != nil {
			return
		}
		h.dispatch(m)
	})
	if err != nil {
		return err
	}
	h.unsub = unsub
	return nil
}

// Stop unsubscribes and closes every socket.
func (h *Hub) Stop() {
	if h.unsub != nil {
		_ = h.unsub()
	}
	h.mu.Lock()
	for s := range h.sockets {
		s.close(websocket.StatusGoingAway, "server shutting down")
	}
	h.mu.Unlock()
}

// Publish sends a message to every node's sockets subscribed to topic in org.
func (h *Hub) Publish(org, topic, typ string, data any, keys map[string]string) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	b, err := json.Marshal(Message{Topic: topic, Type: typ, Data: raw, Org: org, Keys: keys})
	if err != nil {
		return err
	}
	return h.core.PublishCore(bus.RealtimeSubject(org, topic), b)
}

func (h *Hub) dispatch(m Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.sockets {
		s.deliver(m)
	}
}

// Sockets returns the number of open sockets on this node.
func (h *Hub) Sockets() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sockets)
}

// ServeHTTP upgrades GET /v1/admin/ws. Only signed-in users (session cookie) may connect; API keys are
// refused; the Origin must be a trusted origin (S10 §9).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, ok := tenancy.CallerFrom(r.Context())
	if !ok || c.Kind != tenancy.CallerUser {
		writeProblem(w, errs.New(errs.CodeUnauthenticated, "Sign in to use live updates."))
		return
	}
	if r.Header.Get("Origin") == "" {
		writeProblem(w, errs.New(errs.CodeOriginRejected, "A browser Origin is required."))
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		return // Accept already wrote 403 for a foreign origin
	}
	conn.SetReadLimit(MaxMessageBytes)
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	s := &socket{hub: h, conn: conn, caller: c, subs: map[string]sub{}, out: make(chan []byte, 256), cancel: cancel}
	h.mu.Lock()
	h.sockets[s] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sockets, s)
		h.mu.Unlock()
		cancel()
	}()
	go s.writer(ctx)
	s.reader(ctx)
}

func writeProblem(w http.ResponseWriter, err error) {
	p := errs.ProblemFrom(err, "", "")
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.GetStatus())
	_ = json.NewEncoder(w).Encode(p)
}

type sub struct {
	topic  string
	filter Filter
}

type socket struct {
	hub    *Hub
	conn   *websocket.Conn
	caller tenancy.Caller
	mu     sync.Mutex
	subs   map[string]sub // by client subscription ID
	out    chan []byte
	cancel context.CancelFunc
	closed atomic.Bool
}

type clientMsg struct {
	Op     string          `json:"op"`
	ID     string          `json:"id"`
	Topic  string          `json:"topic"`
	Params json.RawMessage `json:"params"`
}

type serverMsg struct {
	Op    string          `json:"op,omitempty"`
	ID    string          `json:"id,omitempty"`
	Topic string          `json:"topic,omitempty"`
	Type  string          `json:"type,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Seq   uint64          `json:"seq,omitempty"`
	Code  string          `json:"code,omitempty"`
}

func (s *socket) send(m serverMsg) {
	b, err := json.Marshal(m)
	if err != nil || s.closed.Load() {
		return
	}
	select {
	case s.out <- b:
	default: // slow client: drop the socket rather than block the hub (it reconnects and refetches)
		s.close(websocket.StatusPolicyViolation, "client too slow")
	}
}

func (s *socket) reader(ctx context.Context) {
	for {
		rctx, cancel := context.WithTimeout(ctx, ClientIdleTimeout)
		typ, data, err := s.conn.Read(rctx)
		cancel()
		if err != nil {
			s.close(websocket.StatusNormalClosure, "")
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			s.send(serverMsg{Op: "error", Code: string(errs.CodeMalformedRequest)})
			continue
		}
		switch m.Op {
		case "ping":
			s.send(serverMsg{Op: "pong"})
		case "sub":
			s.subscribe(m)
		case "unsub":
			s.mu.Lock()
			delete(s.subs, m.ID)
			s.mu.Unlock()
		default:
			s.send(serverMsg{Op: "error", ID: m.ID, Code: string(errs.CodeMalformedRequest)})
		}
	}
}

func (s *socket) subscribe(m clientMsg) {
	s.hub.mu.RLock()
	t, ok := s.hub.topics[m.Topic]
	s.hub.mu.RUnlock()
	switch {
	case m.ID == "" || !ok:
		s.send(serverMsg{Op: "error", ID: m.ID, Code: string(errs.CodeNotFound)})
		return
	case !t.allows(s.caller):
		s.send(serverMsg{Op: "error", ID: m.ID, Code: string(errs.CodeForbidden)})
		return
	}
	var f Filter
	if t.Params != nil {
		var err error
		if f, err = t.Params(m.Params); err != nil {
			s.send(serverMsg{Op: "error", ID: m.ID, Code: string(errs.CodeValidationFailed)})
			return
		}
	}
	if t.Visible != nil {
		caller, inner := s.caller, f
		f = func(msg Message) bool {
			return t.Visible(caller, msg) && (inner == nil || inner(msg))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.subs[m.ID]; !exists && len(s.subs) >= MaxSubscriptions {
		s.send(serverMsg{Op: "error", ID: m.ID, Code: string(errs.CodeRateLimited)})
		return
	}
	s.subs[m.ID] = sub{topic: m.Topic, filter: f}
	s.send(serverMsg{Op: "subscribed", ID: m.ID, Topic: m.Topic})
}

func (s *socket) deliver(m Message) {
	if m.Org != s.caller.OrganizationID {
		return
	}
	s.mu.Lock()
	var ids []string
	for id, sb := range s.subs {
		if sb.topic == m.Topic && (sb.filter == nil || sb.filter(m)) {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.send(serverMsg{ID: id, Topic: m.Topic, Type: m.Type, Data: m.Data, Seq: s.hub.seq.Add(1)})
	}
}

func (s *socket) writer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-s.out:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				s.close(websocket.StatusAbnormalClosure, "write failed")
				return
			}
		}
	}
}

func (s *socket) close(code websocket.StatusCode, reason string) {
	if s.closed.CompareAndSwap(false, true) {
		_ = s.conn.Close(code, reason)
		s.cancel()
	}
}

// KeyFilter returns a Params parser that requires a non-empty string param and filters messages whose
// Keys[name] equals it — e.g. AccountFilter for {"accountId": "acc_…"}.
func KeyFilter(name string, required bool) func(json.RawMessage) (Filter, error) {
	return func(raw json.RawMessage) (Filter, error) {
		var p map[string]string
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
		}
		v := strings.TrimSpace(p[name])
		if v == "" {
			if required {
				return nil, fmt.Errorf("realtime: param %q is required", name)
			}
			return nil, nil
		}
		return func(m Message) bool { return m.Keys[name] == v }, nil
	}
}
