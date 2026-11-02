package webhookverify

import (
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestVerify_invalidTimestamp(t *testing.T) {
	secret, _ := testSecret(t)
	h := http.Header{}
	h.Set(HeaderWebhookID, "id")
	h.Set(HeaderWebhookTimestamp, "nope")
	h.Set(HeaderWebhookSignature, "v1,abc")
	if err := Verify(secret, h, []byte("b"), time.Now(), 0); !errors.Is(err, ErrTimestamp) {
		t.Fatalf("%v", err)
	}
}

func TestVerify_zeroNow(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte("b")
	id := "id"
	ts := time.Now()
	sig, err := Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, formatUnix(ts))
	h.Set(HeaderWebhookSignature, sig)
	if err := Verify(secret, h, body, time.Time{}, DefaultTolerance); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_ignoresNonV1(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte("b")
	id := "id"
	ts := time.Unix(1700000000, 0)
	sig, _ := Sign(secret, id, ts, body)
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, "1700000000")
	h.Set(HeaderWebhookSignature, "v0,deadbeef "+sig)
	if err := Verify(secret, h, body, ts, DefaultTolerance); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_badSigOnly(t *testing.T) {
	secret, _ := testSecret(t)
	h := http.Header{}
	h.Set(HeaderWebhookID, "id")
	h.Set(HeaderWebhookTimestamp, "1700000000")
	h.Set(HeaderWebhookSignature, "notasig v2,x")
	if err := Verify(secret, h, []byte("b"), time.Unix(1700000000, 0), DefaultTolerance); !errors.Is(err, ErrSignature) {
		t.Fatalf("%v", err)
	}
}

func TestSign_invalidSecret(t *testing.T) {
	_, err := Sign("whsec_!!!!", "id", time.Now(), []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSign_emptySecret(t *testing.T) {
	_, err := Sign("whsec_", "id", time.Now(), []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
	// empty base64 of empty
	_, err = Sign("whsec_"+base64.StdEncoding.EncodeToString(nil), "id", time.Now(), []byte("b"))
	if err == nil {
		t.Fatal("expected empty key error")
	}
}

func TestDecodeSecret_noPrefix(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := base64.StdEncoding.EncodeToString(key)
	sig, err := Sign(secret, "id", time.Unix(1, 0), []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(HeaderWebhookID, "id")
	h.Set(HeaderWebhookTimestamp, "1")
	h.Set(HeaderWebhookSignature, sig)
	if err := Verify(secret, h, []byte("b"), time.Unix(1, 0), time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestMatchSignatures_emptyParts(t *testing.T) {
	if err := matchSignatures("  ", "x"); !errors.Is(err, ErrSignature) {
		t.Fatalf("%v", err)
	}
}
