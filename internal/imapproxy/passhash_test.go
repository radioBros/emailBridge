package imapproxy

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	h, err := hashPassword("proxy-secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(h, "proxy-secret-pass") {
		t.Fatal("verify failed")
	}
	if verifyPassword(h, "nope-nope-nope") || verifyPassword("bad", "x") {
		t.Fatal("accepted bad")
	}
}

func TestLimiterAndBudget(t *testing.T) {
	l := newLimiter(1)
	if !l.allow("a") || l.allow("a") {
		t.Fatal("limiter")
	}
	b := newBudget(1)
	if !b.acquire("acc") || b.acquire("acc") {
		t.Fatal("budget")
	}
	if b.active("acc") != 1 {
		t.Fatal(b.active("acc"))
	}
	b.release("acc")
	if b.active("acc") != 0 {
		t.Fatal("release")
	}
	b.release("acc") // idempotent underflow
}
