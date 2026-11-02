package webhook

import (
	"testing"
	"time"
)

func TestBreakerHealthy(t *testing.T) {
	now := time.Now()
	ok := BreakerSample{At: now, Success: true}
	bad := BreakerSample{At: now, Success: false}
	for _, tc := range []struct {
		name string
		b    Breaker
		want bool
	}{
		{"zero value", Breaker{}, true},
		{"closed, successes only", Breaker{State: "closed", Samples: []BreakerSample{ok, ok}}, true},
		{"failure in window", Breaker{State: "closed", Samples: []BreakerSample{ok, bad, ok}}, false},
		{"failure streak", Breaker{State: "closed", Failures: 1}, false},
		{"open", Breaker{State: "open"}, false},
		{"half open", Breaker{State: "halfOpen"}, false},
		{"probe in flight", Breaker{State: "closed", ProbeInFlight: true}, false},
	} {
		if got := tc.b.healthy(); got != tc.want {
			t.Errorf("%s: healthy = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Skipping the store of a success on a healthy breaker must not change any later decision: a run of failures
// trips the breaker after the same number of failures either way.
func TestBreakerSkippedSuccessesKeepDecisions(t *testing.T) {
	now := time.Now()
	full, skipped := Breaker{}, Breaker{}
	for i := range 40 {
		at := now.Add(time.Duration(i) * time.Second)
		full.Record(at, true)
		if !skipped.healthy() {
			skipped.Record(at, true)
		}
	}
	for i := range breakerFailureThreshold {
		at := now.Add(time.Minute + time.Duration(i)*time.Second)
		full.Record(at, false)
		skipped.Record(at, false)
		if full.State != skipped.State {
			t.Fatalf("after %d failures: full=%s skipped=%s", i+1, full.State, skipped.State)
		}
	}
	if full.State != "open" {
		t.Fatalf("breaker did not open: %s", full.State)
	}
}
