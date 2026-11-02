//go:build e2e

package p5

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// DSN hard bounce → message.bounced (+ bounce on message.received).
func TestP5_BounceDSN(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.received", "message.bounced"},
	})
	f.Sink.Reset()
	raw := corpusEML(t, "dsn_hard_001.eml")
	f.injectRaw("INBOX", raw)

	ev := harness.WaitEvent(t, f.Sink, "message.bounced", 90*time.Second)
	harness.VerifyWebhook(t, f.Secret, ev)
	data := eventData(t, ev)
	if bt, _ := data["bounceType"].(string); bt != "hard" {
		t.Fatalf("bounceType=%v data=%v", data["bounceType"], data)
	}
	if rcpt, _ := data["recipient"].(string); rcpt == "" {
		t.Fatalf("missing recipient: %v", data)
	}

	// message.received should carry bounce enrichment (may arrive before or after).
	var found map[string]any
	harness.WaitUntil(t, 30*time.Second, "received with bounce", func() bool {
		for _, r := range harness.FilterEvents(f.Sink.Requests(), "message.received") {
			d := eventData(t, r)
			if b, ok := d["bounce"].(map[string]any); ok && b != nil {
				found = d
				return true
			}
		}
		return false
	})
	if found == nil {
		t.Fatal("message.received missing bounce enrichment")
	}
}

// ARF complaint → message.complained.
func TestP5_ComplaintARF(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.received", "message.complained"},
	})
	f.Sink.Reset()
	f.injectRaw("INBOX", corpusEML(t, "arf_001.eml"))
	ev := harness.WaitEvent(t, f.Sink, "message.complained", 90*time.Second)
	harness.VerifyWebhook(t, f.Secret, ev)
	data := eventData(t, ev)
	if _, ok := data["messageId"]; !ok {
		t.Fatalf("complained missing messageId: %v", data)
	}
	if ft, _ := data["feedbackType"].(string); ft != "" && ft != "abuse" {
		t.Fatalf("unexpected feedbackType=%q", ft)
	}
}

// Auto-reply → message.received.isAutoReply.
func TestP5_AutoReply(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.received"},
	})
	f.Sink.Reset()
	f.injectRaw("INBOX", corpusEML(t, "autoreply_it_ooo_01.eml"))

	var hit sink.Request
	harness.WaitUntil(t, 90*time.Second, "isAutoReply", func() bool {
		for _, r := range f.Sink.Requests() {
			if harness.EventType(r) != "message.received" {
				continue
			}
			d := eventData(t, r)
			if v, ok := d["isAutoReply"].(bool); ok && v {
				hit = r
				return true
			}
		}
		return false
	})
	harness.VerifyWebhook(t, f.Secret, hit)
}
