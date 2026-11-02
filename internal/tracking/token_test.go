package tracking

import (
	"errors"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	exp := time.Now().UTC().Add(time.Hour)
	tok, err := Sign(key, Claims{
		Kind: KindOpen, OrgID: "org_01jbtestorg00000000000000",
		OutboundID: "out_01jbtestout00000000000000", Recipient: "A@B.Test", Index: 0, Exp: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(key, tok, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != KindOpen || c.Recipient != "a@b.test" || c.Index != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestVerifyTamperAndExpiry(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	tok, _ := Sign(key, Claims{
		Kind: KindClick, OrgID: "org_01jbtestorg00000000000000",
		OutboundID: "out_01jbtestout00000000000000", Recipient: "a@b.test", Index: 2,
		Exp: time.Now().UTC().Add(time.Hour),
	})
	if _, err := Verify(key, tok+"x", time.Now()); err == nil {
		t.Fatal("tamper")
	}
	bad := tok[:len(tok)/2] + "AAAA" + tok[len(tok)/2+4:]
	if _, err := Verify(key, bad, time.Now()); err == nil {
		t.Fatal("bad sig")
	}
	expired, _ := Sign(key, Claims{
		Kind: KindUnsub, OrgID: "org_01jbtestorg00000000000000",
		OutboundID: "out_01jbtestout00000000000000", Recipient: "a@b.test",
		Exp: time.Now().UTC().Add(-time.Minute),
	})
	if err := mapTokenErr(errExpired); err == nil {
		t.Fatal("map")
	}
	if _, err := Verify(key, expired, time.Now().UTC()); !errors.Is(err, errExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := Verify(nil, tok, time.Now()); err == nil {
		t.Fatal("nil key")
	}
	if _, err := Sign(nil, Claims{}); err == nil {
		t.Fatal("sign empty")
	}
}

func TestClampTTL(t *testing.T) {
	if clampTTL(0) != defaultTTL || clampTTL(maxTTL+time.Hour) != maxTTL {
		t.Fatal("clamp")
	}
}

func TestSignClickAndUnsub(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	exp := time.Now().UTC().Add(2 * time.Hour)
	for _, kind := range []string{KindClick, KindUnsub} {
		tok, err := Sign(key, Claims{
			Kind: kind, OrgID: "org_01jbtestorg00000000000000",
			OutboundID: "out_01jbtestout00000000000000", Recipient: "z@z.test", Index: 3, Exp: exp,
		})
		if err != nil {
			t.Fatal(err)
		}
		c, err := Verify(key, tok, time.Now().UTC())
		if err != nil || c.Kind != kind {
			t.Fatalf("%s: %+v %v", kind, c, err)
		}
	}
	if _, err := Sign(key, Claims{Kind: "x", OrgID: "o", OutboundID: "o", Recipient: "a@b.c"}); err == nil {
		t.Fatal("bad kind")
	}
}
