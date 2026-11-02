// Package simtest starts an in-process provider simulator for Go tests.
package simtest

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/control"
	simpec "github.com/radiobros/emailbridge/simulator/pec"
)

// Env is a running in-process simulator.
type Env struct {
	Sim    *simulator.Simulator
	Addr   simulator.Addresses
	Client *control.Client
}

// Start runs the simulator on random localhost ports and registers cleanup.
func Start(t *testing.T) *Env {
	t.Helper()
	return StartOpts(t, simulator.Options{})
}

// StartPEC runs the simulator with PEC mode for pec.sim.test (S17 §11).
func StartPEC(t *testing.T) *Env {
	t.Helper()
	return StartOpts(t, simulator.Options{PEC: &simpec.Config{
		Domains: []simpec.Domain{{
			Name: "pec.sim.test", Provider: "Gestore Simulato S.p.A.", // IT
			SystemAddress: "posta-certificata@pec.sim.test",
		}},
		SlowDelay: 20 * time.Millisecond,
	}})
}

// StartOpts runs the simulator with the given options.
func StartOpts(t *testing.T, opts simulator.Options) *Env {
	t.Helper()
	sim, err := simulator.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	addr := sim.Addr()
	return &Env{Sim: sim, Addr: addr, Client: control.NewClient(addr.Control)}
}
