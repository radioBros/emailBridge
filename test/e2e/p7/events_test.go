//go:build e2e

package p7

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func TestP7_Events_SSEAndRealtime(t *testing.T) {
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{})
	time.Sleep(2 * time.Second)
	f.Sink.Reset()

	// SSE live stream.
	sse := openSSE(t, f.Owner, "/v1/events/stream?type=message.received&accountId="+f.AccID)
	defer func() { _ = sse.Body.Close() }()

	subj := fmt.Sprintf("sse-%d", time.Now().UnixNano())
	f.inject(subj, "<"+subj+"@e2e>", "sse body")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	waitSSEEvent(t, sse.Body, "message.received", 45*time.Second)

	// Admin realtime WebSocket: subscribe accounts.state, ping/pong.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Origin", f.Node.Base)
	u, _ := strings.CutPrefix(f.Node.Base, "http")
	wsURL := "ws" + u + "/v1/admin/ws"
	conn, wsRes, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: hdr,
		HTTPClient: f.Owner.C,
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	if wsRes != nil && wsRes.Body != nil { // nil after a successful upgrade
		defer func() { _ = wsRes.Body.Close() }()
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	if err := conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{"op": "ping"})); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pong map[string]any
	_ = json.Unmarshal(data, &pong)
	if pong["op"] != "pong" {
		t.Fatalf("pong: %s", data)
	}
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
		"op": "sub", "id": "s1", "topic": "accounts.state",
	})); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sub map[string]any
	_ = json.Unmarshal(data, &sub)
	if sub["op"] != "subscribed" {
		t.Fatalf("subscribed: %s", data)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
