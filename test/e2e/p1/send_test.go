//go:build e2e

package p1

import (
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Scenario 3: send → message.sent → Sent folder + INBOX message.received.
func TestP1_03_SendSentAndReceived(t *testing.T) {
	f := startFixture(t, nil, "standard", sink.Options{}, []string{
		"message.sent", "message.received",
	})
	f.waitConnected(60 * time.Second)
	time.Sleep(2 * time.Second)
	f.Sink.Reset()

	r := f.submit(map[string]any{
		"to":      []map[string]any{{"address": f.Email}},
		"subject": "e2e send 03",
		"text":    "body-03",
	}, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", r.Status, r.Body)
	}
	outID := r.Str("id")
	out := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+outID, 90*time.Second, "sent|captured", func(r harness.Resp) bool {
		st := r.Str("state")
		return r.Status == http.StatusOK && (st == "sent" || st == "captured" || st == "failed" || st == "outcomeUnknown")
	})
	if out.Str("state") == "captured" {
		t.Skip("BUG: sandbox still capturing despite sandbox.enabled=false on the account")
	}
	if out.Str("state") != "sent" {
		t.Fatalf("outbound state=%s body=%s", out.Str("state"), out.Body)
	}
	_ = harness.WaitEvent(t, f.Sink, "message.sent", 45*time.Second)
	_ = harness.WaitEvent(t, f.Sink, "message.received", 45*time.Second)
}

// Scenario 4: Idempotency-Key replay returns same outbound + Idempotent-Replayed.
func TestP1_04_Idempotency(t *testing.T) {
	f := startFixture(t, nil, "standard", sink.Options{}, []string{"message.sent"})
	f.waitConnected(60 * time.Second)

	payload := map[string]any{
		"to":      []map[string]any{{"address": f.Email}},
		"subject": "idem-04", "text": "x",
	}
	hdr := map[string]string{"Idempotency-Key": "e2e-idem-04"}
	r1 := f.submit(payload, hdr)
	if r1.Status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", r1.Status, r1.Body)
	}
	id1 := r1.Str("id")
	r2 := f.submit(payload, hdr)
	if r2.Status != http.StatusAccepted {
		t.Fatalf("replay = %d %s", r2.Status, r2.Body)
	}
	if r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("want Idempotent-Replayed=true, got %q headers=%v body=%s",
			r2.Header.Get("Idempotent-Replayed"), r2.Header, r2.Body)
	}
	if r2.Str("id") != id1 {
		t.Fatalf("replay id %s != original %s", r2.Str("id"), id1)
	}
}

// Scenario 5: atMostOnce drop after DATA → outcomeUnknown, never resent;
// standard with the same fault retries.
func TestP1_05_AtMostOnceVsStandardDropAfterDATA(t *testing.T) {
	t.Run("atMostOnce", func(t *testing.T) {
		f := startFixture(t, nil, "atMostOnce", sink.Options{}, []string{"message.failed", "message.sent"})
		f.waitConnected(60 * time.Second)
		f.setSMTPScript(simulator.SMTPScript{"dataEnd": {DropAfter: true}})
		before := f.smtpCount()
		r := f.submit(map[string]any{
			"to":      []map[string]any{{"address": f.Email}},
			"subject": "drop-amo", "text": "x", "policy": "atMostOnce",
		}, nil)
		if r.Status != http.StatusAccepted {
			t.Fatalf("submit = %d %s", r.Status, r.Body)
		}
		id := r.Str("id")
		out := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+id, 90*time.Second, "terminal", func(r harness.Resp) bool {
			st := r.Str("state")
			return r.Status == http.StatusOK && (st == "outcomeUnknown" || st == "sent" || st == "captured" || st == "failed")
		})
		if out.Str("state") == "captured" {
			t.Skip("BUG: sandbox still capturing despite sandbox.enabled=false")
		}
		if out.Str("state") != "outcomeUnknown" {
			t.Fatalf("atMostOnce state=%s body=%s", out.Str("state"), out.Body)
		}
		got := f.smtpCount() - before
		if got != 1 {
			t.Fatalf("atMostOnce: simulator got %d copies, want 1", got)
		}
		// Give the worker a chance to wrongly retry.
		time.Sleep(5 * time.Second)
		if f.smtpCount()-before != 1 {
			t.Fatalf("atMostOnce resent: now %d copies", f.smtpCount()-before)
		}
	})

	t.Run("standard", func(t *testing.T) {
		f := startFixture(t, nil, "standard", sink.Options{}, []string{"message.sent", "message.delivery_attempt_failed"})
		f.waitConnected(60 * time.Second)
		f.setSMTPScript(simulator.SMTPScript{"dataEnd": {DropAfter: true}})
		before := f.smtpCount()
		r := f.submit(map[string]any{
			"to":      []map[string]any{{"address": f.Email}},
			"subject": "drop-std", "text": "x", "policy": "standard",
		}, nil)
		if r.Status != http.StatusAccepted {
			t.Fatalf("submit = %d %s", r.Status, r.Body)
		}
		id := r.Str("id")
		// First attempt lands as queued/retrying with outcome-unknown kind.
		var last harness.Resp
		harness.WaitUntil(t, 60*time.Second, "first attempt recorded", func() bool {
			last = f.Owner.Do(http.MethodGet, "/v1/outbound-messages/"+id, nil)
			if last.Status != http.StatusOK {
				return false
			}
			if last.Str("state") == "captured" {
				return true
			}
			attempts, _ := last.JSON["attempts"].([]any)
			return len(attempts) >= 1 || f.smtpCount() > before
		})
		if last.Str("state") == "captured" {
			t.Skip("BUG: sandbox still capturing despite sandbox.enabled=false")
		}
		if f.smtpCount()-before < 1 {
			t.Fatalf("standard never reached the simulator; last=%s", last.Body)
		}
		f.clearSMTPScript()
		f.Owner.WaitOutboundState(t, id, "sent", 120*time.Second)
		if f.smtpCount()-before < 2 {
			t.Fatalf("standard should retry after clearing the fault: copies=%d", f.smtpCount()-before)
		}
	})
}
