//go:build e2e

package p1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Scenario 6: webhook retries → deadLettered; replay keeps webhook-id.
func TestP1_06_WebhookRetriesDeadLetterReplay(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	user := "wh06"
	pass := "sim-secret"
	email := "wh06@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	secret := harness.WebhookSecret()
	sk := harness.StartSink(t, sink.Options{FailTimes: 100, FailStatus: http.StatusInternalServerError})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "wh06-ep", "kind": "http", "url": sk.URL,
		"events": []string{"message.received"},
		"secret": secret,
		"retry": map[string]any{
			"maxAttempts": 3, "backoffBase": "100ms", "backoffJitter": 0.0, "timeout": "2s",
		},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")

	body := harness.AccountCreateBody(t, sim, user, pass, email, "WH06", map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	time.Sleep(2 * time.Second)
	sk.Reset()

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "dlq", "<dlq-06@e2e>", "x"), nil)
	harness.WaitSink(t, sk, 3, 60*time.Second)

	del := owner.WaitDeliveryState(t, "endpointId="+epID+"&limit=20", "deadLettered", 90*time.Second)
	delID, _ := del["id"].(string)
	if delID == "" {
		t.Fatalf("dead letter delivery missing id: %v", del)
	}
	origEvent, _ := del["eventId"].(string)

	sk.SetOptions(sink.Options{Status: http.StatusOK})
	sk.Reset()
	rep := owner.Do(http.MethodPost, "/v1/deliveries/"+delID+"/replay", map[string]any{})
	if rep.Status != http.StatusOK && rep.Status != http.StatusAccepted && rep.Status != http.StatusCreated {
		t.Fatalf("replay = %d %s", rep.Status, rep.Body)
	}
	reqs := harness.WaitSink(t, sk, 1, 45*time.Second)
	req := reqs[len(reqs)-1]
	harness.VerifyWebhook(t, secret, req)
	whID := req.Headers.Get("webhook-id")
	if whID == "" {
		t.Fatal("webhook-id missing on replay")
	}
	if origEvent != "" && whID != origEvent {
		t.Fatalf("replay webhook-id %q != original eventId %q", whID, origEvent)
	}
}

// Scenario 7: route with CEL filter + map — only matching events, mapped body.
func TestP1_07_RouteFilterAndMap(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	user := "rt07"
	pass := "sim-secret"
	email := "rt07@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	secret := harness.WebhookSecret()
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "route07-ep", "kind": "http", "url": sk.URL,
		"events": []string{"message.received", "message.updated"},
		"secret": secret,
		"retry":  defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")

	rte := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name":       "only-received",
		"endpointId": epID,
		"filter":     `type == "message.received"`,
		"map":        `{"mapped": true, "via": "route"}`,
	})
	harness.ExpectStatus(t, rte, http.StatusCreated)

	body := harness.AccountCreateBody(t, sim, user, pass, email, "RT07", map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	time.Sleep(2 * time.Second)
	sk.Reset()

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "Route Subject", "<route-07@e2e>", "body"), nil)
	var mapped sink.Request
	harness.WaitUntil(t, 45*time.Second, "mapped delivery", func() bool {
		for _, r := range sk.Requests() {
			var b map[string]any
			if json.Unmarshal(r.Body, &b) == nil && b["mapped"] == true {
				mapped = r
				return true
			}
		}
		return false
	})
	harness.VerifyWebhook(t, secret, mapped)

	before := len(sk.Requests())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // spans the whole scenario
	defer cancel()
	res, err := sim.Client.InjectMessage(ctx, user, "INBOX",
		harness.RawMessage(email, email, "flag", "<flag-07@e2e>", "z"), nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// wait for this message's own delivery (an earlier message.received is already in the sink): flags set
	// before it is indexed would be part of its first state, not an update
	harness.WaitUntil(t, 45*time.Second, "flag message indexed", func() bool {
		for _, r := range sk.Requests() {
			if bytes.Contains(r.Body, []byte("flag-07@e2e")) {
				return true
			}
		}
		return false
	})
	if err := sim.Client.SetFlags(ctx, user, "INBOX", []uint32{res.UID}, []string{`\Seen`}, "add"); err != nil {
		t.Fatal(err)
	}
	_ = owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/sync", map[string]any{"mode": "full"}) // no CONDSTORE on the simulator: flags need a full pass
	updated := false
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range sk.Requests()[before:] {
			if r.Headers.Get("eb-event-type") == "message.updated" {
				var b map[string]any
				_ = json.Unmarshal(r.Body, &b)
				if b["mapped"] == true {
					t.Fatal("message.updated used the received-only route map")
				}
				updated = true
				break
			}
		}
		if updated {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if !updated {
		t.Fatal("message.updated not delivered after control-API SetFlags + POST …/sync")
	}
}
