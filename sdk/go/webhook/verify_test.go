package webhook

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(key)
	body := []byte(`{"ok":true}`)
	id := "evt_1"
	ts := time.Unix(1700000000, 0)
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
