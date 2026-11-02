package simulator

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// FleetOptions configure load-test message arrival.
type FleetOptions struct {
	Users      int     `json:"users"`
	Password   string  `json:"password"`
	RatePerSec float64 `json:"ratePerSec"` // total message arrival rate across all users
	Mailbox    string  `json:"mailbox"`
	Prefix     string  `json:"prefix"` // username prefix, default "fleet"
}

// StartFleet creates N users and injects messages at ratePerSec until cancelled.
func (sim *Simulator) StartFleet(ctx context.Context, opt FleetOptions) error {
	if opt.Users <= 0 {
		return fmt.Errorf("users must be > 0")
	}
	if opt.Password == "" {
		opt.Password = "fleet"
	}
	if opt.RatePerSec <= 0 {
		opt.RatePerSec = 1
	}
	if opt.Mailbox == "" {
		opt.Mailbox = "INBOX"
	}
	if opt.Prefix == "" {
		opt.Prefix = "fleet"
	}
	for i := 0; i < opt.Users; i++ {
		name := fmt.Sprintf("%s%05d", opt.Prefix, i)
		if sim.store.User(name) == nil {
			if _, err := sim.CreateUser(name, opt.Password); err != nil {
				return err
			}
		}
	}
	if sim.fleetCancel != nil {
		sim.fleetCancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	sim.fleetCancel = cancel

	interval := time.Duration(float64(time.Second) / opt.RatePerSec)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	sim.wg.Add(1)
	go func() {
		defer sim.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				i := rand.Intn(opt.Users) //nolint:gosec // test traffic
				user := fmt.Sprintf("%s%05d", opt.Prefix, i)
				u := sim.store.User(user)
				if u == nil {
					continue
				}
				n++
				raw := []byte(fmt.Sprintf(
					"From: fleet@simulator.test\r\nTo: %s@simulator.test\r\nSubject: fleet %d\r\nMessage-ID: <fleet-%d@sim>\r\nDate: %s\r\n\r\nbody %d\r\n",
					user, n, n, time.Now().Format(time.RFC1123Z), n,
				))
				_, _, _ = u.InjectMessage(opt.Mailbox, raw, nil, time.Now())
			}
		}
	}()
	return nil
}

// StopFleet cancels fleet injection.
func (sim *Simulator) StopFleet() {
	if sim.fleetCancel != nil {
		sim.fleetCancel()
		sim.fleetCancel = nil
	}
}
