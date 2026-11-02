package throttle

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/rules"
)

func TestGateWindowSchedule(t *testing.T) {
	loc := rome()
	w := &Window{TimeZone: "Europe/Rome", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "18:00"}
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, loc)
	g := &Gate{
		Now: func() time.Time { return now },
		Config: func(context.Context, string, string) (Config, error) {
			return Config{WindowsOn: true, Window: w, ThrottleOn: false}, nil
		},
	}
	d := g.BeforeSandbox(context.Background(), Message{OrganizationID: "o", To: []string{"a@b.com"}})
	if d.Action != ActionSchedule || d.Reason != ReasonSendWindow {
		t.Fatalf("%+v", d)
	}
	want := time.Date(2026, 10, 26, 9, 0, 0, 0, loc).UTC()
	if !d.Until.Equal(want) {
		t.Fatalf("until %v want %v", d.Until, want)
	}
}

func TestGateThrottleDelay(t *testing.T) {
	c := NewMemoryCounter()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	g := &Gate{
		Counter: c,
		Now:     func() time.Time { return now },
		Config: func(context.Context, string, string) (Config, error) {
			return Config{ThrottleOn: true, Limits: Limits{DomainPerMinute: 1, RetryAfter: "15s"}}, nil
		},
	}
	m := Message{OrganizationID: "org", To: []string{"u@example.com"}}
	d := g.BeforeSandbox(context.Background(), m)
	if d.Action != ActionAllow {
		t.Fatalf("first %+v", d)
	}
	d = g.BeforeSandbox(context.Background(), m)
	if d.Action != ActionDelay || d.Delay < 15*time.Second {
		t.Fatalf("second %+v", d)
	}
}

func TestGatePreSendAllowHoldReject(t *testing.T) {
	ps, err := rules.NewPreSendEngine(rules.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfgCEL := func(cel string) ConfigFunc {
		return func(context.Context, string, string) (Config, error) {
			return Config{PreSendOn: true, CEL: cel}, nil
		}
	}
	ctx := context.Background()
	m := Message{OrganizationID: "o", AccountID: "a", From: "me@x.com", To: []string{"you@y.com"}, Size: 100}

	g := &Gate{PreSend: ps, Config: cfgCEL(`"allow"`)}
	if d := g.AfterSandbox(ctx, m); d.Action != ActionAllow {
		t.Fatalf("allow %+v", d)
	}

	g.Config = cfgCEL(`{"action":"hold","reason":"review"}`)
	if d := g.AfterSandbox(ctx, m); d.Action != ActionHold || d.Detail != "review" {
		t.Fatalf("hold %+v", d)
	}

	g.Config = cfgCEL(`"reject"`)
	d := g.AfterSandbox(ctx, m)
	if d.Action != ActionReject || d.Problem == nil {
		t.Fatalf("reject %+v", d)
	}

	g.Config = cfgCEL(`size > 1000000`) // wrong type → fail-open
	if d := g.AfterSandbox(ctx, m); d.Action != ActionAllow {
		t.Fatalf("fail-open %+v", d)
	}
}

func TestGateNilAndSwitchOn(t *testing.T) {
	var g *Gate
	if d := g.BeforeSandbox(context.Background(), Message{}); d.Action != ActionAllow {
		t.Fatal(d)
	}
	on := true
	off := false
	if !SwitchOn(features.DefaultRegistry(), switchThrottle, "production", &on) {
		t.Fatal("on")
	}
	if SwitchOn(features.DefaultRegistry(), switchWindows, "production", &off) {
		t.Fatal("off default false with override")
	}
}

func TestMemoryCounterGetExpired(t *testing.T) {
	c := NewMemoryCounter()
	ctx := context.Background()
	_, _ = c.Add(ctx, "k", 3, time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	n, err := c.Get(ctx, "k")
	if err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}
