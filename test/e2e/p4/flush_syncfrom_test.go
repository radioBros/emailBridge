//go:build e2e

package p4

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// POST …/flush rebuilds the index silently — no message.received for pre-existing mail;
// new mail after flush still emits.
func TestP4_FlushSilentRebuild(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.received"}})
	f.Sink.Reset()

	f.inject("pre-flush", fmt.Sprintf("<p4-preflush-%d@e2e>", time.Now().UnixNano()), "before")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	pre := f.waitMsgBySubject("pre-flush", 60*time.Second)
	preID, _ := pre["id"].(string)
	if preID == "" {
		t.Fatal("pre-flush message not indexed")
	}

	f.Sink.Reset()
	before := len(harness.FilterEvents(f.Sink.Requests(), "message.received"))
	fl := f.Owner.Do(http.MethodPost, f.base()+"/flush", nil)
	harness.ExpectStatus(t, fl, http.StatusAccepted)

	f.Owner.WaitJSON(t, f.base()+"/messages?limit=50", 2*time.Minute, "index after flush", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["subject"] == "pre-flush" {
				return true
			}
		}
		return false
	})
	settle := time.Now().Add(3 * time.Second)
	for time.Now().Before(settle) {
		time.Sleep(200 * time.Millisecond)
	}
	after := len(harness.FilterEvents(f.Sink.Requests(), "message.received"))
	if after != before {
		t.Fatalf("flush re-emitted message.received: before=%d after=%d", before, after)
	}

	f.inject("post-flush", fmt.Sprintf("<p4-postflush-%d@e2e>", time.Now().UnixNano()), "after")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	_ = f.waitMsgBySubject("post-flush", 60*time.Second)
}

// sync.syncFrom: messages older than the cut-off are absent from the initial index.
func TestP4_SyncFrom(t *testing.T) {
	sim := harness.StartSim(t)
	user := fmt.Sprintf("p4sf%d", time.Now().UnixNano())
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	oldWhen := time.Now().UTC().Add(-40 * 24 * time.Hour)
	newWhen := time.Now().UTC().Add(-2 * 24 * time.Hour)
	oldRaw := harness.RawMessage(email, email, "Old syncFrom", "<p4-old-sf@e2e>", "old")
	newRaw := harness.RawMessage(email, email, "New syncFrom", "<p4-new-sf@e2e>", "new")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", oldRaw, nil, oldWhen); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", newRaw, nil, newWhen); err != nil {
		t.Fatal(err)
	}

	cut := time.Now().UTC().Add(-14 * 24 * time.Hour)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "sf", "kind": "http", "url": sk.URL, "events": []string{"message.received"},
		"secret": harness.WebhookSecret(), "retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	body := harness.AccountCreateBody(t, sim, user, pass, email, "SyncFrom", map[string]any{"sandbox.enabled": false}, "standard")
	sync, _ := body["sync"].(map[string]any)
	sync["syncFrom"] = cut.Format(time.RFC3339)
	body["sync"] = sync
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 90*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	owner.WaitJSON(t, "/v1/accounts/"+accID+"/messages?limit=50&includeTotal=true", 90*time.Second, "new indexed", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["subject"] == "New syncFrom" {
				return true
			}
		}
		return false
	})
	list := owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/messages?limit=50", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	data, _ := list.JSON["data"].([]any)
	var subjects []string
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		if m != nil {
			if s, _ := m["subject"].(string); s != "" {
				subjects = append(subjects, s)
			}
		}
	}
	for _, s := range subjects {
		if s == "Old syncFrom" {
			t.Fatalf("message older than syncFrom was indexed: %v", subjects)
		}
	}
	foundNew := false
	for _, s := range subjects {
		if s == "New syncFrom" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("message after syncFrom missing: %v", subjects)
	}
}
