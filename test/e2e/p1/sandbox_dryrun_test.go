//go:build e2e

package p1

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Scenario 10: sandbox capture — skip if internal/sandbox is not built yet.
func TestP1_10_SandboxCapture(t *testing.T) {
	if !harness.SandboxExists() {
		t.Skip("internal/sandbox does not exist yet (another agent is building it)")
	}

	// Opt into sandbox on the account; non-production default policy is already capture.
	f := startFixture(t, map[string]any{
		"sandbox.enabled": true,
	}, "standard", sink.Options{}, []string{"message.captured", "message.sent"})
	f.waitConnected(60 * time.Second)

	before := f.smtpCount()
	f.Sink.Reset()
	r := f.submit(map[string]any{
		"to":      []map[string]any{{"address": f.Email}},
		"subject": "capture-10", "text": "should not leave",
	}, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", r.Status, r.Body)
	}
	outID := r.Str("id")
	out := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+outID, 60*time.Second, "captured|sent", func(r harness.Resp) bool {
		st := r.Str("state")
		return r.Status == http.StatusOK && (st == "captured" || st == "sent" || st == "failed")
	})
	if out.Str("state") != "captured" {
		t.Fatalf("want captured, got %s (%s); smtpDelta=%d", out.Str("state"), out.Body, f.smtpCount()-before)
	}
	_ = harness.WaitEvent(t, f.Sink, "message.captured", 45*time.Second)
	if f.smtpCount() != before {
		t.Fatalf("sandbox capture still hit the simulator: before=%d after=%d", before, f.smtpCount())
	}
}

// Scenario 11: dry-run via POST /v1/routes/{id}/test.
func TestP1_11_RouteDryRun(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	oa := owner.Do(http.MethodGet, "/v1/openapi.json", nil)
	if oa.Status != http.StatusOK {
		t.Fatalf("openapi = %d", oa.Status)
	}
	if !strings.Contains(string(oa.Body), "/v1/routes/{routeId}/test") &&
		!strings.Contains(string(oa.Body), "testRoute") {
		t.Skip("POST /v1/routes/{id}/test not present in OpenAPI")
	}

	sk := harness.StartSink(t, sink.Options{})
	secret := harness.WebhookSecret()
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "dry11-ep", "kind": "http", "url": sk.URL,
		"events": []string{"message.received"},
		"secret": secret,
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	rte := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name":       "dry11",
		"endpointId": ep.Str("id"),
		"filter":     `type == "message.received"`,
		"map":        `{"dry": true}`,
	})
	harness.ExpectStatus(t, rte, http.StatusCreated)
	routeID := rte.Str("id")

	r := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{"headers": map[string]any{"subject": []string{"x"}}},
		},
	})
	harness.ExpectStatus(t, r, http.StatusOK)
	if r.JSON["matched"] != true {
		t.Fatalf("dry-run matched=%v body=%s", r.JSON["matched"], r.Body)
	}
}
