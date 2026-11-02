package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type env struct {
	srv *httptest.Server
	hub *Hub
	b   *bus.Bus
}

// setup builds an httpapi server whose late middleware injects the caller chosen by the X-Test-Caller
// header, mounts the realtime module and returns a running test server.
func setup(t *testing.T) *env {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "t", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	t.Cleanup(ts.Close)
	srv, err := httpapi.New(httpapi.Options{PublicURL: ts.URL, AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	srv.AddMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Header.Get("X-Test-Caller") {
			case "viewer":
				r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_v", OrganizationID: "org_1", Role: tenancy.RoleViewer}))
			case "admin":
				r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_a", OrganizationID: "org_1", Role: tenancy.RoleAdmin}))
			case "other":
				r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_o", OrganizationID: "org_2", Role: tenancy.RoleAdmin}))
			case "key":
				r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: "org_1", Scopes: []string{"admin"}}))
			}
			next.ServeHTTP(w, r)
		})
	})
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "t", Log: quiet})
	app.Provide(a, b)
	app.Provide(a, srv)
	m := &Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	if m.Name() != "realtime" || len(m.Roles()) != 1 {
		t.Fatal("identity")
	}
	ts.Config.Handler = srv
	hub, _ := app.MustGet[*Hub](a)
	return &env{srv: ts, hub: hub, b: b}
}

func (e *env) dial(t *testing.T, caller, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("X-Test-Caller", caller)
	if origin != "" {
		h.Set("Origin", origin)
	}
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/v1/admin/ws", &websocket.DialOptions{HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return c, resp, err
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func TestWebSocketFlow(t *testing.T) {
	e := setup(t)
	c, _, err := e.dial(t, "admin", e.srv.URL) //nolint:bodyclose // closed in dial
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, map[string]any{"op": "ping"})
	if m := recv(t, c); m["op"] != "pong" {
		t.Fatalf("pong: %v", m)
	}
	send(t, c, map[string]any{"op": "sub", "id": "s1", "topic": "accounts.state"})
	if m := recv(t, c); m["op"] != "subscribed" || m["id"] != "s1" {
		t.Fatalf("subscribed: %v", m)
	}
	send(t, c, map[string]any{"op": "sub", "id": "s2", "topic": "account.events", "params": map[string]string{"accountId": "acc_1"}})
	recv(t, c)
	// errors: unknown topic, missing required param, bad json, unknown op
	send(t, c, map[string]any{"op": "sub", "id": "x", "topic": "nope"})
	if m := recv(t, c); m["code"] != "not_found" {
		t.Fatalf("unknown topic: %v", m)
	}
	send(t, c, map[string]any{"op": "sub", "id": "y", "topic": "account.events"})
	if m := recv(t, c); m["code"] != "validation_failed" {
		t.Fatalf("missing param: %v", m)
	}
	if err := c.Write(context.Background(), websocket.MessageText, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if m := recv(t, c); m["code"] != "malformed_request" {
		t.Fatalf("bad json: %v", m)
	}
	send(t, c, map[string]any{"op": "dance", "id": "z"})
	if m := recv(t, c); m["code"] != "malformed_request" {
		t.Fatalf("unknown op: %v", m)
	}
	// published messages reach the right subscription only, across NATS
	time.Sleep(100 * time.Millisecond)
	if err := e.hub.Publish("org_1", "accounts.state", "account.state_changed", map[string]string{"accountId": "acc_9", "to": "connected"}, nil); err != nil {
		t.Fatal(err)
	}
	m := recv(t, c)
	if m["id"] != "s1" || m["type"] != "account.state_changed" || m["seq"] == nil || m["org"] != nil {
		t.Fatalf("delivery: %v", m)
	}
	_ = e.hub.Publish("org_2", "accounts.state", "x", 1, nil)                                                    // other org: not delivered
	_ = e.hub.Publish("org_1", "account.events", "message.received", 2, map[string]string{"accountId": "acc_2"}) // filtered out
	_ = e.hub.Publish("org_1", "account.events", "message.received", 3, map[string]string{"accountId": "acc_1"})
	if m := recv(t, c); m["id"] != "s2" || m["data"] != float64(3) {
		t.Fatalf("filtered delivery: %v", m)
	}
	send(t, c, map[string]any{"op": "unsub", "id": "s1"})
	time.Sleep(50 * time.Millisecond)
	_ = e.hub.Publish("org_1", "accounts.state", "after-unsub", 4, nil)
	_ = e.hub.Publish("org_1", "account.events", "marker", 5, map[string]string{"accountId": "acc_1"})
	if m := recv(t, c); m["type"] != "marker" {
		t.Fatalf("unsubscribed topic still delivered: %v", m)
	}
	if e.hub.Sockets() != 1 {
		t.Fatalf("sockets = %d", e.hub.Sockets())
	}
}

func TestWebSocketAuthAndLimits(t *testing.T) {
	e := setup(t)
	// no caller, API key, foreign origin, missing origin
	for _, tc := range []struct {
		caller, origin string
		status         int
	}{
		{"", e.srv.URL, 401}, {"key", e.srv.URL, 401}, {"admin", "https://evil.example", 403}, {"admin", "", 403},
	} {
		_, resp, err := e.dial(t, tc.caller, tc.origin) //nolint:bodyclose // closed in dial
		if err == nil || resp == nil || resp.StatusCode != tc.status {
			code := 0
			if resp != nil {
				code = resp.StatusCode
			}
			t.Errorf("caller=%q origin=%q: err=%v status=%d want %d", tc.caller, tc.origin, err, code, tc.status)
		}
	}
	// viewer may not subscribe to logs.tail
	c, _, err := e.dial(t, "viewer", e.srv.URL) //nolint:bodyclose // closed in dial
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, map[string]any{"op": "sub", "id": "l", "topic": "logs.tail"})
	if m := recv(t, c); m["code"] != "forbidden" {
		t.Fatalf("viewer logs.tail: %v", m)
	}
	// subscription limit
	for i := 0; i < MaxSubscriptions; i++ {
		send(t, c, map[string]any{"op": "sub", "id": "s" + string(rune('A'+i%26)) + string(rune('a'+i/26)), "topic": "accounts.state"})
		recv(t, c)
	}
	send(t, c, map[string]any{"op": "sub", "id": "over", "topic": "accounts.state"})
	if m := recv(t, c); m["code"] != "rate_limited" {
		t.Fatalf("limit: %v", m)
	}
	// other organisation's socket gets nothing from org_1
	o, _, err := e.dial(t, "other", e.srv.URL) //nolint:bodyclose // closed in dial
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.CloseNow() }()
	send(t, o, map[string]any{"op": "sub", "id": "s", "topic": "accounts.state"})
	recv(t, o)
	_ = e.hub.Publish("org_1", "accounts.state", "x", 1, nil)
	_ = e.hub.Publish("org_2", "accounts.state", "mine", 1, nil)
	if m := recv(t, o); m["type"] != "mine" {
		t.Fatalf("cross-org leak: %v", m)
	}
	// shutdown closes sockets
	e.hub.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := o.Read(ctx); err == nil {
		t.Fatal("socket must close on hub stop")
	}
}

type badCore struct{}

func (badCore) PublishCore(string, []byte) error { return errors.New("down") }
func (badCore) SubscribeCore(string, func(string, []byte)) (func() error, error) {
	return nil, errors.New("down")
}

func TestHubRegistryAndErrors(t *testing.T) {
	h := NewHub(badCore{}, nil, []string{"https://gw.example.com", "::bad"})
	if err := h.Register(Topic{Name: "x"}); err == nil {
		t.Fatal("topic without permission")
	}
	if err := h.Register(Topic{Name: "x", Permission: tenancy.PermRead, ReadScope: "accounts"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Register(Topic{Name: "x", Permission: tenancy.PermRead, ReadScope: "accounts"}); err == nil {
		t.Fatal("duplicate topic")
	}
	if err := h.Start(); err == nil {
		t.Fatal("start must surface subscribe errors")
	}
	if err := h.Publish("org", "x", "t", 1, nil); err == nil {
		t.Fatal("publish error")
	}
	if err := h.Publish("org", "x", "t", make(chan int), nil); err == nil {
		t.Fatal("unmarshalable data")
	}
	h.Stop()
	f, err := KeyFilter("accountId", false)(nil)
	if err != nil || f != nil {
		t.Fatal("optional param absent")
	}
	if _, err := KeyFilter("accountId", false)(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("bad params")
	}
}

func TestReadTopicsNeedAReadScope(t *testing.T) {
	h := NewHub(badCore{}, nil, nil)
	if err := h.Register(Topic{Name: "y", Permission: tenancy.PermRead}); err == nil {
		t.Fatal("a read topic without a read scope must be refused")
	}
	topic := Topic{Name: "z", Permission: tenancy.PermRead, ReadScope: "events"}
	if topic.allows(tenancy.Caller{Kind: tenancy.CallerAPIKey, Scopes: []string{"outbound:write"}}) {
		t.Fatal("an unrelated scope must not subscribe")
	}
	if !topic.allows(tenancy.Caller{Kind: tenancy.CallerAPIKey, Scopes: []string{"events:read"}}) {
		t.Fatal("events:read subscribes")
	}
	logs := Topic{Name: "l", Permission: tenancy.PermLogsRead}
	if !logs.allows(tenancy.Caller{Kind: tenancy.CallerUser, Role: tenancy.RoleAdmin}) {
		t.Fatal("non-read topics keep their permission check")
	}
}

// TestTopicVisibleFiltersPerCaller: a topic's Visible hook hides messages from subscribers it rejects (W75:
// instance-scope notifications reach instance owners only), while other messages still arrive.
func TestTopicVisibleFiltersPerCaller(t *testing.T) {
	e := setup(t)
	if err := e.hub.Register(Topic{
		Name: "visible.test", Permission: tenancy.PermRead, ReadScope: "accounts",
		Visible: func(c tenancy.Caller, msg Message) bool {
			return msg.Keys["scope"] != "instance" || c.Role == tenancy.RoleAdmin
		},
	}); err != nil {
		t.Fatal(err)
	}
	subscribe := func(caller string) *websocket.Conn {
		c, _, err := e.dial(t, caller, e.srv.URL) //nolint:bodyclose // closed in dial
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		send(t, c, map[string]any{"op": "sub", "id": "v", "topic": "visible.test"})
		if m := recv(t, c); m["op"] != "subscribed" {
			t.Fatalf("subscribe %s: %v", caller, m)
		}
		return c
	}
	admin, viewer := subscribe("admin"), subscribe("viewer")
	time.Sleep(100 * time.Millisecond)
	_ = e.hub.Publish("org_1", "visible.test", "hidden", 1, map[string]string{"scope": "instance"})
	_ = e.hub.Publish("org_1", "visible.test", "shown", 2, map[string]string{"scope": "org_1"})
	if m := recv(t, admin); m["type"] != "hidden" {
		t.Fatalf("admin first message: %v", m)
	}
	if m := recv(t, admin); m["type"] != "shown" {
		t.Fatalf("admin second message: %v", m)
	}
	if m := recv(t, viewer); m["type"] != "shown" {
		t.Fatalf("viewer must only get the visible message: %v", m)
	}
}
