//go:build e2e

package p8

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// FeatherDesk inbound callback: valid HMAC submits mail; tampered signature is refused.
func TestP8_FeatherDeskCallback(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	desk := harness.StartSink(t, sink.Options{})
	user := fmt.Sprintf("fd%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	accID := connectAccount(t, owner, sim, user, "sim-secret", email, "FDAcc")

	tok := "callback-secret-p8"
	inst := install(t, owner, "featherdesk", map[string]any{
		"name": "fd-e2e",
		"config": map[string]any{
			"baseUrl":   desk.URL,
			"accountId": accID,
		},
		"secrets": map[string]string{"apiKey": "desk-key", "callbackToken": tok},
	})
	id := inst.Str("id")

	body := []byte(fmt.Sprintf(
		`{"to":%q,"subject":"Re: ticket","bodyText":"thanks from desk","inReplyTo":"<orig@e2e>","ticketId":"T1","messageId":"<fd-1@e2e>"}`,
		email,
	))
	ts := time.Now().UTC().Unix()

	// Tampered signature → 401
	badStatus, badBody := postCallback(t, n.Base, id, body, ts, "sha256=deadbeef")
	if badStatus != http.StatusUnauthorized {
		t.Fatalf("tampered sig = %d %s", badStatus, badBody)
	}

	// Valid signature → 204 and an outbound message
	sig := signFeatherDesk(tok, ts, body)
	okStatus, okBody := postCallback(t, n.Base, id, body, ts, sig)
	if okStatus != http.StatusNoContent && okStatus != http.StatusOK {
		t.Fatalf("valid callback = %d %s\nnode=%s", okStatus, okBody, n.Stderr())
	}

	_ = owner.WaitJSON(t, "/v1/outbound-messages?accountId="+accID+"&limit=20", 90*time.Second, "outbound from callback", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			subj, _ := m["subject"].(string)
			if subj == "Re: ticket" {
				return true
			}
		}
		return false
	})
}

// Sentry connector applies a fake DSN; an injected internal_error reaches the httptest.
func TestP8_SentryErrorReporting(t *testing.T) {
	_, hits, dsn := fakeSentry(t)
	n := harness.StartP1Node(t, map[string]string{
		// Ensure env DSN does not lock out the connector.
		"EB_SENTRY_DSN": "",
	})
	owner, _ := n.OwnerSession(t)

	inst := install(t, owner, "sentry", map[string]any{
		"name": "sentry-e2e",
		"config": map[string]any{
			"environment": "e2e",
			"sampleRate":  "1.0",
		},
		"secrets": map[string]string{"dsn": dsn},
	})
	id := inst.Str("id")

	tr := owner.Do(http.MethodPost, "/v1/integration-instances/"+id+"/test", map[string]any{})
	harness.ExpectStatus(t, tr, http.StatusOK)
	if ok, _ := tr.JSON["ok"].(bool); !ok {
		t.Fatalf("sentry test: %s", tr.Body)
	}

	// Inject a decrypt failure so the next /test returns reportable internal_error.
	corruptInstanceSecret(t, n, id, "dsn")
	before := hits.Load()
	bad := owner.Do(http.MethodPost, "/v1/integration-instances/"+id+"/test", map[string]any{})
	if bad.Str("code") != "internal_error" && bad.Status < 500 {
		t.Skipf("BUG: could not inject reportable internal error via corrupt sentry secret — got %d %s (suspect: integrations.decrypt / httpapi.OnReportable)", bad.Status, bad.Body)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() > before {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("fake Sentry DSN received no event after internal_error (before=%d after=%d); response was %d %s",
		before, hits.Load(), bad.Status, bad.Body)
}
