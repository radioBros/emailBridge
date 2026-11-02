//go:build integration

package queues

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/submit"
)

type coreRec struct {
	mu   sync.Mutex
	subs []string
	msgs []realtime.Message
}

func (c *coreRec) PublishCore(subject string, data []byte) error {
	var m realtime.Message
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs = append(c.subs, subject)
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *coreRec) SubscribeCore(string, func(string, []byte)) (func() error, error) {
	return func() error { return nil }, nil
}

// The module registers the queues.updates topic on the hub and a cancel publishes queue.changed to the
// organisation's realtime subject (W72 §6).
func TestIntegrationRealtimeTopicAndPublish(t *testing.T) {
	h := newHarness(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	core := &coreRec{}
	hub := realtime.NewHub(core, quiet, nil)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "api-2", Log: quiet})
	app.Provide(a, h.db)
	app.Provide(a, hub)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Log: quiet}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := hub.Register(realtime.Topic{Name: topicUpdates, Permission: "read", ReadScope: "outbound"}); err == nil {
		t.Fatal("queues.updates not registered by the module")
	}
	id := h.outbound(orgA, accA, submit.StateQueued, 0, false)
	h.srv = srv
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{id}}, admin(orgA)); w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.subs) != 1 || core.subs[0] != bus.RealtimeSubject(orgA, topicUpdates) {
		t.Fatalf("published to %v", core.subs)
	}
	if msg := core.msgs[0]; msg.Type != typeChanged || msg.Keys["queue"] != QueueSending || msg.Org != orgA {
		t.Fatalf("message: %+v", msg)
	}
	// Filter by queue param works and an absent param lets everything through.
	f, err := realtime.KeyFilter("queue", false)(nil)
	if err != nil || f != nil {
		t.Fatalf("no-param filter: %v", err)
	}
}
