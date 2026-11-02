package webhook

import (
	"testing"
	"time"
)

func TestBreakerTripsAndHalfOpenProbe(t *testing.T) {
	now := time.Now().UTC()
	state := Breaker{}
	for range 9 {
		state.Record(now, false)
	}
	if state.State != "closed" {
		t.Fatalf("opened early: %+v", state)
	}
	state.Record(now, false)
	if state.State != "open" || state.NextProbeAt == nil || state.OpenedAt == nil {
		t.Fatalf("did not open: %+v", state)
	}
	if allowed, _ := state.Allow(now); allowed {
		t.Fatal("advanced before deadline")
	}
	if allowed, wait := state.Allow(state.NextProbeAt.Add(time.Millisecond)); !allowed || wait != 0 {
		t.Fatal("probe not allowed")
	}
	if state.State != "halfOpen" || !state.ProbeInFlight {
		t.Fatalf("half open = %+v", state)
	}
	if allowed, _ := state.Allow(now); allowed {
		t.Fatal("second probe claimed")
	}
	state.Record(now, false)
	if state.State != "open" || state.ProbeInFlight {
		t.Fatalf("failed probe = %+v", state)
	}
	state.State, state.ProbeInFlight = "halfOpen", true
	state.Record(now, true)
	if state.State != "closed" || len(state.Samples) != 0 {
		t.Fatalf("successful probe = %+v", state)
	}
}

func TestBreakerRatioDefaultsAndPruning(t *testing.T) {
	now := time.Now().UTC()
	state := Breaker{}
	for i := range 60 {
		state.Record(now.Add(time.Duration(i)*time.Millisecond), true)
	}
	if len(state.Samples) != breakerMinSamples {
		t.Fatalf("sample cap = %d", len(state.Samples))
	}
	state = Breaker{
		State: "closed",
		Samples: []BreakerSample{
			{At: now.Add(-2 * time.Hour), Success: false},
			{At: now, Success: true},
		},
	}
	state.Record(now, true)
	if len(state.Samples) != 2 {
		t.Fatalf("unexpected samples: %+v", state.Samples)
	}
	state.prune(now.Add(time.Hour))
	if len(state.Samples) != 0 {
		t.Fatalf("stale samples = %+v", state.Samples)
	}
	state = Breaker{State: "invalid"}
	if allowed, _ := state.Allow(now); !allowed || state.State != "closed" {
		t.Fatalf("normalize = %+v", state)
	}
}

func TestBreakerOpensAtHalfFailuresInWindowAndCapsBackoff(t *testing.T) {
	now := time.Now().UTC()
	state := Breaker{}
	for i := range breakerMinSamples {
		state.Failures = 0
		state.Record(now.Add(time.Duration(i)*time.Millisecond), i%2 == 0)
	}
	if state.State != "open" {
		t.Fatalf("ratio did not open: %+v", state)
	}
	state.OpenCount = 20
	state.open(now)
	if state.NextProbeAt.Sub(now) != breakerMaxOpen {
		t.Fatalf("backoff not capped: %+v", state)
	}
}
