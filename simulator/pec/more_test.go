package pec

import (
	"testing"
	"time"

	intpec "github.com/radiobros/emailbridge/internal/pec"
)

func TestEmitForcedVariants(t *testing.T) {
	for _, typ := range []string{
		intpec.ReceiptDeliveryNotice,
		intpec.ReceiptNonAcceptance,
		intpec.ReceiptVirus,
		intpec.ReceiptDelivery, // default branch via emitForced
	} {
		t.Run(typ, func(t *testing.T) {
			store := &memStore{}
			e := newEng(t, store, nil)
			e.ForceNextReceipt("sender@pec.sim.test", typ)
			e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
				[]string{"rcpt@pec.sim.test"}, []byte(origBody))
			n := 2
			msgs := waitN(t, store, "sender@pec.sim.test", n)
			sm := parseAll(t, msgs, e.Anchors())
			if typ == intpec.ReceiptDelivery {
				if findType(sm, intpec.ReceiptDelivery) == nil {
					t.Fatalf("%v", typesOf(sm))
				}
				return
			}
			if findType(sm, typ) == nil {
				t.Fatalf("want %s in %v", typ, typesOf(sm))
			}
		})
	}
}

func TestDefaultsAndNilPaths(t *testing.T) {
	store := &memStore{}
	cfg := &Config{Domains: []Domain{{Name: "pec.sim.test"}}} // defaults for provider/system/now/sleep
	e, err := New(cfg, store.inject)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	if e.DomainOf("x@pec.sim.test").SystemAddress != "posta-certificata@pec.sim.test" {
		t.Fatal(e.DomainOf("x@pec.sim.test"))
	}
	if e.DomainOf("x@other.test") != nil {
		t.Fatal("missing domain")
	}
	if (*Engine)(nil).DomainOf("x") != nil || (*Engine)(nil).CAPEM() != nil {
		t.Fatal("nil engine")
	}
	if err := e.WriteCAPEM(""); err != nil {
		t.Fatal(err)
	}
	// body without Message-ID
	body := []byte("From: sender@pec.sim.test\r\nTo: a@example.com\r\nSubject: x\r\n\r\nz\r\n")
	e.OnPECSend("sender@pec.sim.test", "", []string{"a@example.com"}, body)
	_ = waitN(t, store, "sender@pec.sim.test", 1)

	e.OnOrdinaryToPEC("a@example.com", []string{"plain@example.com", "rcpt@pec.sim.test"}, body)
	_ = waitN(t, store, "rcpt@pec.sim.test", 1)

	e.sleep(0)
	if e.stopped() {
		t.Fatal("engine should still be running")
	}
}

func TestDefaultSleepRespectsStop(t *testing.T) {
	store := &memStore{}
	cfg := &Config{
		Domains:   []Domain{{Name: "pec.sim.test"}},
		SlowDelay: time.Hour,
	}
	e, err := New(cfg, store.inject)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		e.sleep(time.Hour)
		close(done)
	}()
	e.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sleep did not return after Stop")
	}
}

func TestBreakSignatureFallback(t *testing.T) {
	raw := []byte("xxxx daticert yyyy")
	out := breakSignature(raw)
	if out[0] == raw[0] && string(out) == string(raw) {
		// must have flipped the 'd' in daticert
		t.Fatal("expected flip")
	}
}

func TestConfigSlowDelayDefault(t *testing.T) {
	var c *Config
	if c.slowDelay() != 2*time.Second {
		t.Fatal(c.slowDelay())
	}
	if c.now().IsZero() {
		t.Fatal("now")
	}
	c2 := &Config{}
	if c2.slowDelay() != 2*time.Second {
		t.Fatal(c2.slowDelay())
	}
}

func TestOnPECSendNilEngine(t *testing.T) {
	(*Engine)(nil).OnPECSend("a", "a", nil, nil)
	(*Engine)(nil).OnOrdinaryToPEC("a", nil, nil)
	(*Engine)(nil).Reset()
}

func TestHeaderGetBad(t *testing.T) {
	if headerGet([]byte("not a message"), "Subject") != "" {
		t.Fatal("bad")
	}
	if normalizeUser("no-angle") != "no-angle" {
		t.Fatal(normalizeUser("no-angle"))
	}
}

func TestInjectGenerateErrorPath(t *testing.T) {
	// processSend with empty from/sender domain mismatch — sender not on PEC domain
	store := &memStore{}
	e := newEng(t, store, nil)
	e.OnPECSend("nobody@other.test", "nobody@other.test", []string{"rcpt@pec.sim.test"}, []byte(origBody))
	time.Sleep(50 * time.Millisecond)
	if len(store.of("nobody@other.test")) != 0 {
		t.Fatal("should no-op")
	}
}
