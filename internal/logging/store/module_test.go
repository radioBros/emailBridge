package store

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/logging"
)

func TestModuleWiresStoreHubAndResponder(t *testing.T) {
	ctx := context.Background()
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sys := logging.New(logging.Options{Node: "n1", Role: "api", Stdout: io.Discard, Fallback: io.Discard, FlushEvery: 10 * time.Millisecond})
	defer func() { _ = sys.Close(ctx) }()
	a := app.New(app.Options{NodeID: "n1"})
	app.Provide(a, b)
	m := &Module{System: sys, Options: Options{Dir: t.TempDir(), SweepEvery: -1}}
	if m.Name() != "logging.store" || m.Roles() != nil {
		t.Fatal("identity")
	}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Get[*Store](a); !ok {
		t.Fatal("store not provided")
	}
	hub, _ := app.Get[*Hub](a)
	tailed := make(chan LogRecord, 4)
	cancel := hub.Subscribe(Filter{Categories: []logging.Category{logging.CatSync}}, func(r LogRecord) { tailed <- r })
	defer cancel()

	sys.For(logging.CatSync, "sync.idle").Info("idle started", "mailbox", "INBOX")
	if err := sys.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-tailed:
		if r.Msg != "idle started" {
			t.Fatalf("tail %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail tee did not deliver")
	}
	f := Filter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 10, Categories: []logging.Category{logging.CatSync}}
	req, _ := json.Marshal(Request{Op: OpQuery, Filter: &f})
	raw, err := b.ScatterGather(ctx, bus.LogsQuerySubject, req, 1, 5*time.Second)
	if err != nil || len(raw) != 1 {
		t.Fatalf("scatter %d %v", len(raw), err)
	}
	var rep Reply
	if err := json.Unmarshal(raw[0], &rep); err != nil || rep.Node != "n1" || len(rep.Records) != 1 {
		t.Fatalf("reply %s %v", raw[0], err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.ScatterGather(ctx, bus.LogsQuerySubject, req, 1, 200*time.Millisecond); len(raw) != 0 {
		t.Fatalf("stopped module must not answer: %s", raw[0])
	}
	if err := (&Module{}).Stop(ctx); err != nil {
		t.Fatal("stop before start")
	}
	if err := (&Module{}).Start(ctx, a); err == nil {
		t.Fatal("module without system")
	}
	bad := &Module{System: sys, Options: Options{Dir: ""}}
	if err := bad.Start(ctx, app.New(app.Options{NodeID: "n1"})); err == nil {
		t.Fatal("bad options")
	}
}

func TestModuleWithoutBus(t *testing.T) {
	ctx := context.Background()
	sys := logging.New(logging.Options{Node: "solo", Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(ctx) }()
	a := app.New(app.Options{NodeID: "solo"})
	m := &Module{System: sys, Options: Options{Dir: t.TempDir(), SweepEvery: -1}}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	node, ok := app.Get[*Node](a)
	if !ok || node.Pub != nil {
		t.Fatal("node without bus")
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
