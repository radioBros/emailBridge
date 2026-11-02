package msa

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	h, err := hashPassword("super-secret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(h, "super-secret-password") {
		t.Fatal("verify failed")
	}
	if verifyPassword(h, "wrong-password-xx") {
		t.Fatal("wrong password accepted")
	}
	if verifyPassword("bad", "x") || verifyPassword("v1$zz$yy", "x") {
		t.Fatal("garbage hash accepted")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2)
	if !l.allow("a") {
		t.Fatal("first should pass")
	}
	if !l.allow("a") {
		t.Fatal("second should pass")
	}
	if l.allow("a") {
		t.Fatal("third should fail")
	}
	if !l.allow("b") {
		t.Fatal("other key should pass")
	}
	if !newLimiter(0).allow("x") { // defaulted
		t.Fatal("default limiter")
	}
}
