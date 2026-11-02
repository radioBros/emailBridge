package webhookverify

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func testSecret(t *testing.T) (secret string, key []byte) {
	t.Helper()
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(key), key
}

func TestSignVerify_roundTrip(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte(`{"hello":"world"}`)
	id := "msg_test"
	ts := time.Unix(1_700_000_000, 0)
	sig, err := Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, "1700000000")
	h.Set(HeaderWebhookSignature, sig)
	if err := Verify(secret, h, body, ts, DefaultTolerance); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_rotationAnyMatch(t *testing.T) {
	secret, _ := testSecret(t)
	other, _ := testSecret(t)
	body := []byte("payload")
	id := "evt_1"
	ts := time.Unix(1_700_000_100, 0)
	good, err := Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := Sign(other, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, "1700000100")
	h.Set(HeaderWebhookSignature, bad+" "+good)
	if err := Verify(secret, h, body, ts, DefaultTolerance); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_timestampTolerance(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte("x")
	id := "evt_2"
	ts := time.Unix(1_700_000_000, 0)
	sig, _ := Sign(secret, id, ts, body)
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, "1700000000")
	h.Set(HeaderWebhookSignature, sig)

	if err := Verify(secret, h, body, ts.Add(6*time.Minute), DefaultTolerance); !errors.Is(err, ErrTimestamp) {
		t.Fatalf("got %v", err)
	}
	if err := Verify(secret, h, body, ts.Add(-6*time.Minute), DefaultTolerance); !errors.Is(err, ErrTimestamp) {
		t.Fatalf("got %v", err)
	}
	if err := Verify(secret, h, body, ts.Add(4*time.Minute), DefaultTolerance); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_tamperedBody(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte("good")
	id := "evt_3"
	ts := time.Unix(1_700_000_000, 0)
	sig, _ := Sign(secret, id, ts, body)
	h := http.Header{}
	h.Set(HeaderWebhookID, id)
	h.Set(HeaderWebhookTimestamp, "1700000000")
	h.Set(HeaderWebhookSignature, sig)
	if err := Verify(secret, h, []byte("bad"), ts, DefaultTolerance); !errors.Is(err, ErrSignature) {
		t.Fatalf("got %v", err)
	}
}

func TestVerify_missingHeaders(t *testing.T) {
	err := Verify("whsec_YQ==", http.Header{}, []byte("b"), time.Now(), 0)
	if !errors.Is(err, ErrMissingHeaders) {
		t.Fatalf("got %v", err)
	}
}

func TestVerifyBodyOnly(t *testing.T) {
	secret := "raw-shared-secret"
	body := []byte(`{"a":1}`)
	sig := SignBodyOnly(secret, body)
	h := http.Header{}
	h.Set(HeaderEBSignature, sig)
	if err := VerifyBodyOnly(secret, h, body); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBodyOnly(secret, h, []byte("nope")); !errors.Is(err, ErrSignature) {
		t.Fatalf("got %v", err)
	}
	if err := VerifyBodyOnly(secret, http.Header{}, body); !errors.Is(err, ErrMissingHeaders) {
		t.Fatalf("got %v", err)
	}
}

func TestMiddleware(t *testing.T) {
	secret, _ := testSecret(t)
	body := []byte(`{"ok":true}`)
	id := "evt_mw"
	ts := time.Now()
	sig, _ := Sign(secret, id, ts, body)

	var gotBody []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = b
		w.WriteHeader(http.StatusNoContent)
	})
	h := Middleware(secret, next)

	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	req.Header.Set(HeaderWebhookID, id)
	req.Header.Set(HeaderWebhookTimestamp, formatUnix(ts))
	req.Header.Set(HeaderWebhookSignature, sig)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d", rr.Code)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body restored = %q", gotBody)
	}

	bad := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, bad)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr2.Code)
	}
}

func formatUnix(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}
