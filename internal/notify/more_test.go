package notify

import (
	"context"
	"testing"
	"time"
)

func TestChannelsNilGuards(t *testing.T) {
	var c *Channels
	c.OnRaised(context.Background(), nil, true, false)
	c.OnResolved(context.Background(), nil)
	c = &Channels{}
	c.OnRaised(context.Background(), &Notification{ID: "ntf_x", Scope: "org_1", Key: "k"}, true, false)
	c.OnResolved(context.Background(), &Notification{ID: "ntf_x", Scope: ScopeInstance, Key: "k", ResolvedAt: ptrTime(time.Now())})
	_ = c.now()
	_ = (&Engine{}).now()
	_ = (&Watchers{}).now()
	_ = (&Watchers{}).log()
	_ = (&Metrics{}).Collector()
	(&Metrics{}).Refresh(context.Background(), nil)
	if Default() != nil {
		t.Fatal("no module started: Default must be nil")
	}
	var e *Engine
	e.Raise(context.Background(), Problem{})
	e.Resolve(context.Background(), "", "")
	e.CaptureError(context.Background(), nil, nil)
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestAsStringSlice(t *testing.T) {
	if len(asStringSlice([]string{"a"})) != 1 {
		t.Fatal()
	}
	if len(asStringSlice([]any{"a", 1})) != 1 {
		t.Fatal()
	}
	if asStringSlice(1) != nil {
		t.Fatal()
	}
}
