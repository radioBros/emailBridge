// Package oracle cross-checks webhookverify against the official Standard Webhooks Go library (SP7).
package oracle_test

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/radiobros/emailbridge/pkg/webhookverify"
)

func TestSignaturesMatchOfficialLibrary(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	secret := "whsec_" + base64.StdEncoding.EncodeToString(key)
	body := []byte(`{"type":"message.received","id":"evt_1"}`)
	id := "evt_01test"
	ts := time.Now().UTC().Truncate(time.Second)

	ours, err := webhookverify.Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}

	wh, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := wh.Sign(id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	if ours != theirs {
		t.Fatalf("signature mismatch\n ours=%s\ntheirs=%s", ours, theirs)
	}

	h := http.Header{}
	h.Set(webhookverify.HeaderWebhookID, id)
	h.Set(webhookverify.HeaderWebhookTimestamp, strconv.FormatInt(ts.Unix(), 10))
	h.Set(webhookverify.HeaderWebhookSignature, ours)

	if err := webhookverify.Verify(secret, h, body, ts, webhookverify.DefaultTolerance); err != nil {
		t.Fatalf("our Verify: %v", err)
	}
	if err := wh.Verify(body, h); err != nil {
		t.Fatalf("official Verify of our signature: %v", err)
	}

	// Official signs, we verify.
	h2 := http.Header{}
	h2.Set(webhookverify.HeaderWebhookID, id)
	h2.Set(webhookverify.HeaderWebhookTimestamp, strconv.FormatInt(ts.Unix(), 10))
	h2.Set(webhookverify.HeaderWebhookSignature, theirs)
	if err := webhookverify.Verify(secret, h2, body, ts, webhookverify.DefaultTolerance); err != nil {
		t.Fatalf("our Verify of official signature: %v", err)
	}
}
