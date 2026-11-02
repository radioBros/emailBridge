// Package pec is the PEC mode of the provider simulator (S17 §11, P9.1).
package pec

import "time"

// Domain is one simulated PEC provider domain.
type Domain struct {
	Name          string // e.g. "pec.sim.test"
	Provider      string // gestore-emittente
	SystemAddress string // e.g. "posta-certificata@pec.sim.test"
}

// Config enables PEC mode on the simulator.
type Config struct {
	Domains []Domain
	// SlowDelay is the wait after preavviso-errore-consegna for local-part "slow" (default 2s).
	SlowDelay time.Duration
	// Now overrides the clock (tests). Nil → time.Now.
	Now func() time.Time
	// Sleep overrides time.Sleep (tests). Must return promptly when the engine is stopped.
	Sleep func(d time.Duration)
}

func (c *Config) slowDelay() time.Duration {
	if c != nil && c.SlowDelay > 0 {
		return c.SlowDelay
	}
	return 2 * time.Second
}

func (c *Config) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
