package exports

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	pt := []byte("hello export")
	blob, err := sealAESGCM(key, pt)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob[:4]) != magic {
		t.Fatalf("magic %q", blob[:4])
	}
	got, err := openAESGCM(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("got %q", got)
	}
}

func TestOpenBadKey(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	blob, err := sealAESGCM(key, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Repeat([]byte{2}, 32)
	if _, err := openAESGCM(bad, blob); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v", err)
	}
	if _, err := openAESGCM(key[:16], blob); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short key: %v", err)
	}
	if _, err := openAESGCM(key, []byte("short")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short blob: %v", err)
	}
	if _, err := sealAESGCM([]byte("short"), nil); err == nil {
		t.Fatal("expected key size error")
	}
}

func TestSignVerify(t *testing.T) {
	key := []byte("signing-key-32-bytes-long!!!!!!")
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	tok, err := Sign(key, "org_01hzzzzzzzzzzzzzzzzzzzzz", "exp_01hzzzzzzzzzzzzzzzzzzzzz", exp)
	if err != nil {
		t.Fatal(err)
	}
	org, id, err := VerifyToken(key, tok, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	if err != nil || org == "" || id == "" {
		t.Fatalf("%q %q %v", org, id, err)
	}
	if _, _, err := VerifyToken(key, tok, time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, errExpired) {
		t.Fatalf("want expired, got %v", err)
	}
	if _, _, err := VerifyToken(key, "bad", exp); !errors.Is(err, errBadToken) {
		t.Fatalf("bad token: %v", err)
	}
	if err := mapTokenErr(errExpired); err == nil {
		t.Fatal("expected mapped err")
	}
	if err := mapTokenErr(errBadToken); err == nil {
		t.Fatal("expected mapped err")
	}
	if clampTTL(0) != defaultTTL || clampTTL(48*time.Hour) != maxSignedTTL {
		t.Fatal("clamp")
	}
	if _, err := Sign(nil, "a", "b", exp); err == nil {
		t.Fatal("expected sign error")
	}
}
