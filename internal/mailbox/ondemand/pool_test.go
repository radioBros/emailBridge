package ondemand

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func TestAcquireErrBusy(t *testing.T) {
	err := AcquireErr(ErrBusy)
	if errs.CodeOf(err) != errs.CodeConnectionBusy {
		t.Fatalf("code %s", errs.CodeOf(err))
	}
	if AcquireErr(errors.New("x")) == nil {
		t.Fatal("passthrough")
	}
}

func TestPoolReuseAndBusy(t *testing.T) {
	sim := simtest.Start(t)
	user := "pooluser"
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	acc := &ebsync.Account{
		ID: "acc_01hpool000000000000000000", OrganizationID: "org_x",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	p := &Pool{Idle: time.Minute, Metrics: NewMetrics()}
	l1, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire(ctx, acc); !errors.Is(err, ErrBusy) {
		t.Fatalf("want busy, got %v", err)
	}
	l1.Release()
	l2, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Client == nil {
		t.Fatal("nil client")
	}
	l2.Discard()
	p.Close()
}

func TestPoolIdleSweep(t *testing.T) {
	sim := simtest.Start(t)
	user := "idleuser"
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	acc := &ebsync.Account{
		ID: "acc_01hidle000000000000000000", OrganizationID: "org_x",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	p := &Pool{Idle: 20 * time.Millisecond, Metrics: NewMetrics()}
	l, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	time.Sleep(40 * time.Millisecond)
	p.Sweep()
	p.mu.Lock()
	s := p.byAcct[acc.ID]
	p.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		t.Fatal("expected idle close")
	}
}

func TestMetricsCollectors(t *testing.T) {
	m := NewMetrics()
	if len(m.Collectors()) != 4 {
		t.Fatal(m.Collectors())
	}
	if (*Metrics)(nil).Collectors() != nil {
		t.Fatal("nil metrics")
	}
}

func TestPoolRunAndDefaultIdle(t *testing.T) {
	sim := simtest.Start(t)
	user := "runuser"
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	acc := &ebsync.Account{
		ID: "acc_01hrun00000000000000000000", OrganizationID: "org_x",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	p := &Pool{Idle: 0, Metrics: NewMetrics()} // default idle
	if p.idleFor() != defaultIdle {
		t.Fatal(p.idleFor())
	}
	p.Idle = 40 * time.Millisecond
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); p.Run(rctx) }()
	l, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byAcct != nil {
		s := p.byAcct[acc.ID]
		if s != nil {
			s.mu.Lock()
			if s.client != nil {
				t.Fatal("expected closed after Run")
			}
			s.mu.Unlock()
		}
	}
}

func TestPoolReleaseIdempotentAndFingerprint(t *testing.T) {
	sim := simtest.Start(t)
	user := "fpuser"
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	acc := &ebsync.Account{
		ID: "acc_01hfp000000000000000000000", OrganizationID: "org_x",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	p := &Pool{Idle: time.Minute, Metrics: NewMetrics()}
	l, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	l.Release() // idempotent
	(*Lease)(nil).Release()
	(*Lease)(nil).Discard()

	// change fingerprint → take closes old
	l2, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
	acc2 := *acc
	acc2.IMAPUser = "other"
	if _, err := p.Acquire(ctx, &acc2); err == nil {
		t.Fatal("expected dial failure for unknown user")
	}
	p.Close()
	p.Close() // idempotent
}

func TestPoolDialWithEgress(t *testing.T) {
	sim := simtest.Start(t)
	user := "eguser"
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	acc := &ebsync.Account{
		ID: "acc_01heg000000000000000000000", OrganizationID: "org_x",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	p := &Pool{Dialer: egress.NewDialer(egress.Policy{}), Idle: time.Minute}
	l, err := p.Acquire(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	l.Discard()
	p.Close()
}
