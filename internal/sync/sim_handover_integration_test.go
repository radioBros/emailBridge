//go:build integration

package sync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/simulator/simtest"
	"github.com/radiobros/emailbridge/test/containers"
)

// TestIntegrationSimulatorSessionHandover isolates the simulator from the engine: one session idles on INBOX
// and logs out, a second one selects INBOX and idles right away (what a hand-off does), then mail arrives.
// The second session must be told (EXISTS) every time.
func TestIntegrationSimulatorSessionHandover(t *testing.T) {
	sim := simtest.Start(t)
	ctx := context.Background()
	user := fmt.Sprintf("h%d", time.Now().UnixNano())
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	opts := imapc.Options{Addr: sim.Addr.IMAPPlain, TLS: imapc.TLSNone, Username: user, Password: "pw", CommandTimeout: 5 * time.Second}
	handover(t, opts, func(i int) error {
		_, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw(fmt.Sprintf("h %d", i), fmt.Sprintf("<h-%d@x>", i)), nil, time.Now())
		return err
	})
}

// The same sequence against Dovecot: tells a simulator bug apart from a client bug.
func TestIntegrationDovecotSessionHandover(t *testing.T) {
	d := containers.DovecotShared(t)
	ctx := context.Background()
	user := fmt.Sprintf("h%d", time.Now().UnixNano())
	opts := imapc.Options{Addr: d.IMAPS, TLS: imapc.TLSImplicit, InsecureSkipVerify: true, Username: user, Password: d.Password, CommandTimeout: 5 * time.Second}
	w, err := imapc.Dial(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Logout(ctx) }()
	handover(t, opts, func(i int) error {
		_, err := w.Append(ctx, "INBOX", nil, time.Now(), raw(fmt.Sprintf("h %d", i), fmt.Sprintf("<h-%d@x>", i)))
		return err
	})
}

func handover(t *testing.T, opts imapc.Options, inject func(i int) error) {
	t.Helper()
	ctx := context.Background()
	for i := range 50 {
		a, err := imapc.Dial(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Select(ctx, "INBOX", true); err != nil {
			t.Fatal(err)
		}
		actx, acancel := context.WithCancel(ctx)
		adone := make(chan struct{})
		go func() { defer close(adone); _ = a.IdleUntil(actx, time.Minute, nil) }()
		time.Sleep(20 * time.Millisecond)
		acancel()
		<-adone
		_ = a.Logout(ctx)

		b, err := imapc.Dial(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Select(ctx, "INBOX", true); err != nil {
			t.Fatal(err)
		}
		for len(b.Updates()) > 0 { // stale EXISTS from the SELECT itself
			<-b.Updates()
		}
		bctx, bcancel := context.WithCancel(ctx)
		bdone := make(chan struct{})
		go func() { defer close(bdone); _ = b.IdleUntil(bctx, time.Minute, nil) }()
		if err := inject(i); err != nil {
			t.Fatal(err)
		}
		got := false
		deadline := time.After(2 * time.Second)
	wait:
		for {
			select {
			case u := <-b.Updates():
				if u.Kind == imapc.UpdateExists {
					got = true
					break wait
				}
			case <-deadline:
				break wait
			}
		}
		bcancel()
		<-bdone
		_ = b.Logout(ctx)
		if !got {
			t.Fatalf("iteration %d: the new session was not told about new mail", i)
		}
	}
}
