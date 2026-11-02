package webhook

import "time"

const (
	breakerWindow           = 5 * time.Minute
	breakerMinSamples       = 50
	breakerFailureThreshold = 10
	breakerInitialOpen      = time.Minute
	breakerMaxOpen          = 30 * time.Minute
)

// Allow reports whether one attempt may start and, when denied, how long until the next probe.
func (b *Breaker) Allow(now time.Time) (bool, time.Duration) {
	b.normalize()
	switch b.State {
	case "open":
		if b.NextProbeAt != nil && now.Before(*b.NextProbeAt) {
			return false, b.NextProbeAt.Sub(now)
		}
		b.State = "halfOpen"
		b.ProbeInFlight = true
		return true, 0
	case "halfOpen":
		if b.ProbeInFlight {
			return false, breakerInitialOpen
		}
		b.ProbeInFlight = true
		return true, 0
	default:
		return true, 0
	}
}

// healthy reports a closed breaker whose window holds no failure: a success then changes no decision the
// breaker can make (Failures stays 0, windowFailed needs failures), so it need not be stored.
func (b *Breaker) healthy() bool {
	if b.State != "" && b.State != "closed" {
		return false
	}
	if b.Failures != 0 || b.ProbeInFlight {
		return false
	}
	for _, sample := range b.Samples {
		if !sample.Success {
			return false
		}
	}
	return true
}

// Record updates the state after one real attempt. Breaker deferrals do not call Record.
func (b *Breaker) Record(now time.Time, success bool) {
	b.normalize()
	b.prune(now)
	b.Samples = append(b.Samples, BreakerSample{At: now, Success: success})
	if len(b.Samples) > breakerMinSamples {
		b.Samples = append([]BreakerSample(nil), b.Samples[len(b.Samples)-breakerMinSamples:]...)
	}
	if success {
		b.Failures = 0
		if b.State == "halfOpen" {
			b.close()
		}
		return
	}

	b.Failures++
	if b.State == "halfOpen" {
		b.open(now)
		return
	}
	if b.Failures >= breakerFailureThreshold || b.windowFailed() {
		b.open(now)
	}
}

func (b *Breaker) normalize() {
	switch b.State {
	case "closed", "open", "halfOpen":
	default:
		b.State = "closed"
	}
}

func (b *Breaker) prune(now time.Time) {
	cutoff := now.Add(-breakerWindow)
	first := 0
	for first < len(b.Samples) && b.Samples[first].At.Before(cutoff) {
		first++
	}
	if first > 0 {
		b.Samples = append([]BreakerSample(nil), b.Samples[first:]...)
	}
}

func (b *Breaker) windowFailed() bool {
	if len(b.Samples) < breakerMinSamples {
		return false
	}
	failures := 0
	for _, sample := range b.Samples[len(b.Samples)-breakerMinSamples:] {
		if !sample.Success {
			failures++
		}
	}
	return failures*2 >= breakerMinSamples
}

func (b *Breaker) open(now time.Time) {
	b.State = "open"
	b.ProbeInFlight = false
	b.OpenCount++
	wait := breakerInitialOpen
	for i := 1; i < b.OpenCount && wait < breakerMaxOpen; i++ {
		wait *= 2
	}
	if wait > breakerMaxOpen {
		wait = breakerMaxOpen
	}
	opened, probe := now.UTC(), now.Add(wait).UTC()
	b.OpenedAt, b.NextProbeAt = &opened, &probe
}

func (b *Breaker) close() {
	b.State = "closed"
	b.OpenedAt = nil
	b.NextProbeAt = nil
	b.ProbeInFlight = false
	b.OpenCount = 0
	b.Failures = 0
	b.Samples = nil
}
