//go:build e2e

package p1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Scenario 1: register → initial sync silent → new mail → message.received with requested headers + signature.
func TestP1_01_RegisterSyncSilentThenReceived(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	user := "sync01"
	pass := "sim-secret"
	email := "sync01@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)
	// Pre-existing mail must not produce message.received.
	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "old", "<old-01@e2e>", "old body"), nil)

	secret := harness.WebhookSecret()
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "sync01-ep", "kind": "http", "url": sk.URL,
		"events":  []string{"message.received"},
		"secret":  secret,
		"headers": []map[string]any{{"name": "X-E2E-Hook", "value": "requested"}},
		"payload": map[string]any{"text": "plain", "headers": []string{"Subject", "Message-ID"}},
		"retry":   defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	body := harness.AccountCreateBody(t, sim, user, pass, email, "Sync01", map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")

	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	// Give deliveries a moment; initial sync must stay silent.
	time.Sleep(3 * time.Second)
	if got := harness.FilterEvents(sk.Requests(), "message.received"); len(got) != 0 {
		t.Fatalf("initial sync emitted message.received: %d", len(got))
	}

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "Fresh Subject", "<new-01@e2e>", "hello"), nil)
	req := harness.WaitEvent(t, sk, "message.received", 45*time.Second)
	if req.Headers.Get("X-E2E-Hook") != "requested" {
		t.Fatalf("custom header missing: %v", req.Headers)
	}
	harness.VerifyWebhook(t, secret, req)

	var env map[string]any
	if err := json.Unmarshal(req.Body, &env); err != nil {
		t.Fatal(err)
	}
	data, _ := env["data"].(map[string]any)
	hdrs, _ := data["headers"].(map[string]any)
	if hdrs == nil {
		// Top-level subject is present; data.headers was requested via endpoint payload but omitted.
		if subj, _ := data["subject"].(string); subj != "Fresh Subject" {
			t.Fatalf("subject missing too: %s", req.Body)
		}
		t.Fatalf("endpoint payload.headers=[Subject,Message-ID] but message.received has no data.headers: %s", req.Body)
	}
	if _, ok := hdrs["Subject"]; !ok {
		if _, ok2 := hdrs["subject"]; !ok2 {
			t.Fatalf("Subject not in requested headers: %v", hdrs)
		}
	}
	_ = accID
}

// Scenario 2: flag → message.updated; delete → message.deleted; UIDVALIDITY reset → mailbox.reset.
func TestP1_02_FlagDeleteUIDValidity(t *testing.T) {
	f := startFixture(t, nil, "standard", sink.Options{}, []string{
		"message.received", "message.updated", "message.deleted", "mailbox.reset",
	})
	f.waitConnected(60 * time.Second)
	time.Sleep(2 * time.Second)
	f.Sink.Reset()

	mid := "<flag-02@e2e>"
	res := f.Sim.InjectRaw(t, f.User, "INBOX", harness.RawMessage(f.Email, f.Email, "flagme", mid, "x"), nil)
	_ = harness.WaitEvent(t, f.Sink, "message.received", 45*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // spans the whole scenario
	defer cancel()
	var bugs []string
	if err := f.Sim.Client.SetFlags(ctx, f.User, "INBOX", []uint32{res.UID}, []string{`\Seen`}, "add"); err != nil {
		t.Fatal(err)
	}
	_ = f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{"mode": "full"}) // the simulator has no CONDSTORE: flag/UID diffs need a full pass (S5 §4)
	updated := false
	updDeadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(updDeadline) {
		if len(harness.FilterEvents(f.Sink.Requests(), "message.updated")) > 0 {
			updated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !updated {
		bugs = append(bugs, "control-API SetFlags(\\Seen) + POST …/sync never produced message.updated")
	}

	if err := f.Sim.Client.SetFlags(ctx, f.User, "INBOX", []uint32{res.UID}, []string{`\Deleted`}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sim.Client.Expunge(ctx, f.User, "INBOX", []uint32{res.UID}); err != nil {
		t.Fatal(err)
	}
	// Nudge a sync pass in case IDLE did not surface the control-API expunge.
	_ = f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{"mode": "full"}) // the simulator has no CONDSTORE: flag/UID diffs need a full pass (S5 §4)
	deleted := false
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if len(harness.FilterEvents(f.Sink.Requests(), "message.deleted")) > 0 {
			deleted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !deleted {
		bugs = append(bugs, "control-API flag+expunge + POST …/sync never produced message.deleted")
	}

	// Seed a message that will be re-indexed silently after UIDVALIDITY changes. It is new mail, so it is
	// reported first (IDLE); count after that.
	preCount := len(harness.FilterEvents(f.Sink.Requests(), "message.received"))
	f.Sim.InjectRaw(t, f.User, "INBOX", harness.RawMessage(f.Email, f.Email, "pre-reset", "<reset-02@e2e>", "y"), nil)
	preDeadline := time.Now().Add(30 * time.Second)
	for len(harness.FilterEvents(f.Sink.Requests(), "message.received")) == preCount && time.Now().Before(preDeadline) {
		time.Sleep(200 * time.Millisecond)
	}
	beforeRecv := len(harness.FilterEvents(f.Sink.Requests(), "message.received"))
	if _, err := f.Sim.Client.RecreateMailbox(ctx, f.User, "INBOX"); err != nil {
		t.Fatal(err)
	}
	// Put content back under the new UIDVALIDITY before/as sync rebuilds.
	f.Sim.InjectRaw(t, f.User, "INBOX", harness.RawMessage(f.Email, f.Email, "post-reset", "<reset-02b@e2e>", "z"), nil)
	if sr := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{}); sr.Status != http.StatusAccepted {
		t.Fatalf("POST …/sync = %d %s", sr.Status, sr.Body)
	}
	resetOK := false
	resetDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(resetDeadline) {
		if len(harness.FilterEvents(f.Sink.Requests(), "mailbox.reset")) > 0 {
			resetOK = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !resetOK {
		bugs = append(bugs, "RecreateMailbox + POST …/sync never produced mailbox.reset")
	} else {
		time.Sleep(4 * time.Second)
		afterRecv := len(harness.FilterEvents(f.Sink.Requests(), "message.received"))
		if afterRecv != beforeRecv {
			bugs = append(bugs, fmt.Sprintf("UIDVALIDITY re-index not silent: received before=%d after=%d", beforeRecv, afterRecv))
		}
	}
	if len(bugs) > 0 {
		t.Fatal(strings.Join(bugs, "; "))
	}
}
