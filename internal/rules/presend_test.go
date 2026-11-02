package rules

import (
	"context"
	"testing"
	"time"
)

func TestPreSendEngineActions(t *testing.T) {
	e, err := NewPreSendEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in := PreSendInput{
		Envelope: map[string]any{"from": "a@b.com", "to": []any{"c@gmail.com"}},
		Size:     50,
		Account:  map[string]any{"id": "acc_1"},
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	r, err := e.Eval(ctx, "", in)
	if err != nil || r.Action != PreSendAllow {
		t.Fatalf("empty %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `"allow"`, in)
	if err != nil || r.Action != PreSendAllow {
		t.Fatalf("allow %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `{"action":"hold","reason":"wait"}`, in)
	if err != nil || r.Action != PreSendHold || r.Reason != "wait" {
		t.Fatalf("hold %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `size < 10 ? "reject" : "allow"`, in)
	if err != nil || r.Action != PreSendAllow {
		t.Fatalf("cond allow %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `size < 100 ? "reject" : "allow"`, in)
	if err != nil || r.Action != PreSendReject {
		t.Fatalf("cond reject %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `domain(envelope.from) == "b.com" ? "allow" : "reject"`, in)
	if err != nil || r.Action != PreSendAllow {
		t.Fatalf("domain %+v %v", r, err)
	}

	r, err = e.Eval(ctx, `this is not cel!!!`, in)
	if err == nil || r.Action != PreSendAllow {
		t.Fatalf("bad expr %+v %v", r, err)
	}
}

func TestNormalizeAction(t *testing.T) {
	if normalizeAction("HOLD") != PreSendHold || normalizeAction("nope") != PreSendAllow {
		t.Fatal("normalize")
	}
}
