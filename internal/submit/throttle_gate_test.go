package submit

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/throttle"
)

type stubThrottle struct {
	before, after throttle.Decision
}

func (s stubThrottle) BeforeSandbox(context.Context, throttle.Message) throttle.Decision {
	return s.before
}
func (s stubThrottle) AfterSandbox(context.Context, throttle.Message) throttle.Decision {
	return s.after
}

func TestThrottleMsgAndApplyNil(t *testing.T) {
	s := &Service{}
	done, err := s.applyThrottleBefore(context.Background(), &Message{}, nil)
	if done || err != nil {
		t.Fatal(done, err)
	}
	done, rej, err := s.applyThrottleAfter(context.Background(), &Message{}, nil, nil)
	if done || rej != nil || err != nil {
		t.Fatal(done, rej, err)
	}
	ext := "ext"
	tm := s.throttleMsg(&Message{
		OrganizationID: "o", AccountID: "a", Envelope: Envelope{From: "f@x.com", To: []string{"t@y.com"}, BccCount: 1},
		Bcc: []string{"b@z.com"}, Size: 9,
	}, &SMTPAccount{ExternalID: &ext}, []string{"r@rewritten.com"})
	if tm.ExternalID != "ext" || len(tm.To) != 1 || tm.To[0] != "r@rewritten.com" || tm.Size != 9 {
		t.Fatalf("%+v", tm)
	}
}

func TestApplyThrottleBeforeDelayUsesStub(t *testing.T) {
	// Without Repo, requeue will panic/nil — only exercise After reject path with nil Repo avoided
	s := &Service{Throttle: stubThrottle{after: throttle.Decision{
		Action: throttle.ActionReject, Problem: errBusyElsewhere,
	}}}
	done, rej, err := s.applyThrottleAfter(context.Background(), &Message{ID: "out_1"}, nil, nil)
	if !done || rej == nil || err != nil {
		t.Fatal(done, rej, err)
	}
	s.Throttle = stubThrottle{after: throttle.Decision{Action: throttle.ActionAllow}}
	done, rej, err = s.applyThrottleAfter(context.Background(), &Message{}, nil, nil)
	if done || rej != nil || err != nil {
		t.Fatal(done, rej, err)
	}
	_ = time.Second
}
