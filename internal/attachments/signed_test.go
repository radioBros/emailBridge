package attachments

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := []byte("test-signing-key-32-bytes-long!!")
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tok, err := Sign(key, "org_01habcdefghijklmnopqrstuvw", "att_01habcdefghijklmnopqrstuvw", exp)
	if err != nil {
		t.Fatal(err)
	}
	org, att, err := VerifyToken(key, tok, exp.Add(-time.Minute))
	if err != nil || org != "org_01habcdefghijklmnopqrstuvw" || att != "att_01habcdefghijklmnopqrstuvw" {
		t.Fatalf("got %s %s %v", org, att, err)
	}
}

func TestSignVerifyTamperAndExpiry(t *testing.T) {
	key := []byte("k")
	exp := time.Now().UTC().Add(time.Hour)
	tok, err := Sign(key, "org_x", "att_y", exp)
	if err != nil {
		t.Fatal(err)
	}
	dot := strings.IndexByte(tok, '.')
	tampered := tok[:dot+1] + "AAAA" + tok[dot+5:]
	if _, _, err := VerifyToken(key, tampered, time.Now()); err == nil {
		t.Fatal("expected tamper failure")
	}
	if errs.CodeOf(mapTokenErr(errBadToken)) != errs.CodeSignedURLInvalid {
		t.Fatal(mapTokenErr(errBadToken))
	}
	expired := time.Now().UTC().Add(-time.Hour)
	tok2, _ := Sign(key, "org_x", "att_y", expired)
	if _, _, err := VerifyToken(key, tok2, time.Now()); err == nil {
		t.Fatal("expected expiry")
	}
	if errs.CodeOf(mapTokenErr(errExpired)) != errs.CodeSignedURLExpired {
		t.Fatal(mapTokenErr(errExpired))
	}
	if _, err := Sign(nil, "o", "a", exp); err == nil {
		t.Fatal("empty key")
	}
	if _, _, err := VerifyToken(key, "not-a-token", time.Now()); err == nil {
		t.Fatal("bad form")
	}
	if !strings.Contains(tok, ".") {
		t.Fatal(tok)
	}
}

func TestClampTTL(t *testing.T) {
	if clampTTL(0) != defaultTTL {
		t.Fatal(clampTTL(0))
	}
	if clampTTL(48*time.Hour) != maxSignedTTL {
		t.Fatal(clampTTL(48 * time.Hour))
	}
}
