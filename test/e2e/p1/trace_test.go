//go:build e2e

package p1

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

var traceparentRe = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-0[01]$`)

// traceOf splits a W3C traceparent into trace and span ID ("" when malformed).
func traceOf(tp string) (traceID, spanID string) {
	m := traceparentRe.FindStringSubmatch(tp)
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}

// P2.8 (S12 §10): trace context runs end to end, with OpenTelemetry export off.
//   - Mail arriving in the mailbox starts a trace (sync fetch); the event carries it, and the webhook request
//     carries a child of it (same trace ID, the delivery attempt's own span ID).
//   - A send continues the caller's trace: API request → stored on the outbound message → SMTP send →
//     message.sent event → webhook, all on the trace ID the client sent.
func TestP2_08_TraceContextEndToEnd(t *testing.T) {
	f := startFixture(t, nil, "standard", sink.Options{}, []string{"message.sent", "message.received"})
	f.waitConnected(60 * time.Second)
	time.Sleep(2 * time.Second)
	f.Sink.Reset()

	check := func(t *testing.T, req sink.Request, wantTrace string) {
		t.Helper()
		hTrace, hSpan := traceOf(req.Headers.Get("traceparent"))
		if hTrace == "" {
			t.Fatalf("webhook traceparent header %q is not W3C", req.Headers.Get("traceparent"))
		}
		var env struct {
			TraceParent string `json:"traceparent"`
		}
		if err := json.Unmarshal(req.Body, &env); err != nil {
			t.Fatal(err)
		}
		eTrace, eSpan := traceOf(env.TraceParent)
		if eTrace == "" {
			t.Fatalf("event traceparent %q is not W3C", env.TraceParent)
		}
		if hTrace != eTrace {
			t.Fatalf("webhook trace %s != event trace %s", hTrace, eTrace)
		}
		if hSpan == eSpan {
			t.Fatal("the webhook request must carry the delivery attempt's own span, not the event's")
		}
		if wantTrace != "" && eTrace != wantTrace {
			t.Fatalf("event trace %s, want the caller's %s", eTrace, wantTrace)
		}
	}

	t.Run("received", func(t *testing.T) {
		f.inject("trace in", "<trace-in@e2e>", "hello")
		check(t, harness.WaitEvent(t, f.Sink, "message.received", 45*time.Second), "")
	})

	t.Run("sent", func(t *testing.T) {
		f.Sink.Reset()
		const callerTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
		r := f.submit(map[string]any{
			"to": []map[string]any{{"address": f.Email}}, "subject": "trace out", "text": "x",
		}, map[string]string{"traceparent": "00-" + callerTrace + "-00f067aa0ba902b7-01"})
		if r.Status != http.StatusAccepted {
			t.Fatalf("submit = %d %s", r.Status, r.Body)
		}
		if tr, _ := traceOf(r.Header.Get("Traceparent")); tr != callerTrace {
			t.Fatalf("response traceparent %q does not continue the caller's trace", r.Header.Get("Traceparent"))
		}
		f.Owner.WaitOutboundState(t, r.Str("id"), "sent", 90*time.Second)
		check(t, harness.WaitEvent(t, f.Sink, "message.sent", 45*time.Second), callerTrace)
	})
}
