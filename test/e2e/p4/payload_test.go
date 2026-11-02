//go:build e2e

package p4

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// webSafeHtml, calendarEvents and replyText appear on message.received when payload opts in.
func TestP4_ContentPayloadFields(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.received"},
		payload: map[string]any{
			"text": "html", "webSafeHtml": true, "calendarEvents": true, "replyText": true, "attachments": true,
		},
	})
	f.Sink.Reset()

	htmlRaw := htmlScriptMsg(f.Email, f.Email, "HTML xss", "<p4-html@e2e>",
		`<p>Hello</p><script>alert(1)</script><img src="https://evil.example/x.png" onerror="x">`)
	f.injectRaw("INBOX", htmlRaw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data := eventData(t, ev)
	wsh, _ := data["webSafeHtml"].(string)
	if wsh == "" {
		t.Fatalf("webSafeHtml missing: %s", ev.Body)
	}
	if strings.Contains(strings.ToLower(wsh), "<script") || strings.Contains(strings.ToLower(wsh), "onerror") {
		t.Fatalf("webSafeHtml not sanitised: %s", wsh)
	}
	if strings.Contains(wsh, "evil.example") {
		t.Fatalf("remote image should be stripped by default: %s", wsh)
	}

	f.Sink.Reset()
	cal := calendarInvite(f.Email, f.Email, "Meet invite", "<p4-cal@e2e>")
	f.injectRaw("INBOX", cal, nil, time.Time{})
	ev2 := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data2 := eventData(t, ev2)
	cals, _ := data2["calendarEvents"].([]any)
	if len(cals) < 1 {
		t.Fatalf("calendarEvents missing: %s", ev2.Body)
	}
	ce, _ := cals[0].(map[string]any)
	if ce["summary"] != "Standup" && ce["uid"] == nil {
		t.Fatalf("calendar event unexpected: %v", ce)
	}

	f.Sink.Reset()
	rep := replyQuoted(f.Email, f.Email, "Re: old", "<p4-reply@e2e>", "Brand new answer only")
	f.injectRaw("INBOX", rep, nil, time.Time{})
	ev3 := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data3 := eventData(t, ev3)
	rt, _ := data3["replyText"].(string)
	if rt == "" {
		t.Fatalf("replyText missing: %s", ev3.Body)
	}
	if !strings.Contains(rt, "Brand new answer only") {
		t.Fatalf("replyText missing new text: %q", rt)
	}
	if strings.Contains(rt, "old quoted line") || strings.Contains(rt, "wrote:") {
		t.Fatalf("replyText kept quote: %q", rt)
	}
}

// Organisation / account allow switch off suppresses payload fields even when the endpoint asks.
func TestP4_PayloadAllowSwitchOff(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		switches: map[string]any{
			"mailbox.webSafeHtml":    false,
			"mailbox.calendarEvents": false,
			"inbound.replyText":      false,
		},
		events: []string{"message.received"},
		payload: map[string]any{
			"webSafeHtml": true, "calendarEvents": true, "replyText": true, "text": "html",
		},
	})
	f.Sink.Reset()

	htmlRaw := htmlScriptMsg(f.Email, f.Email, "No HTML", "<p4-nohtml@e2e>", `<p>Hi</p><script>x</script>`)
	f.injectRaw("INBOX", htmlRaw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data := eventData(t, ev)
	if _, ok := data["webSafeHtml"]; ok {
		t.Fatalf("webSafeHtml present with allow switch off: %s", ev.Body)
	}

	f.Sink.Reset()
	cal := calendarInvite(f.Email, f.Email, "No cal", "<p4-nocal@e2e>")
	f.injectRaw("INBOX", cal, nil, time.Time{})
	ev2 := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data2 := eventData(t, ev2)
	if _, ok := data2["calendarEvents"]; ok {
		t.Fatalf("calendarEvents present with allow switch off: %s", ev2.Body)
	}

	f.Sink.Reset()
	rep := replyQuoted(f.Email, f.Email, "No reply", "<p4-noreply@e2e>", "Fresh")
	f.injectRaw("INBOX", rep, nil, time.Time{})
	ev3 := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data3 := eventData(t, ev3)
	if _, ok := data3["replyText"]; ok {
		t.Fatalf("replyText present with allow switch off: %s", ev3.Body)
	}
}

// attachmentMax: 1 keeps a single attachment entry in the delivered payload.
func TestP4_AttachmentMax(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events:  []string{"message.received"},
		payload: map[string]any{"attachments": true, "attachmentMax": 1},
	})
	f.Sink.Reset()

	raw := multipartAtts(f.Email, f.Email, "Max atts", fmt.Sprintf("<p4-max-%d@e2e>", time.Now().UnixNano()),
		[][2]string{{"one.txt", "1"}, {"two.txt", "2"}, {"three.txt", "3"}})
	f.injectRaw("INBOX", raw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data := eventData(t, ev)
	atts, _ := data["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachmentMax=1 want 1 entry, got %d: %s", len(atts), ev.Body)
	}
}

// Organisation-level allow switch off (PATCH /v1/settings before account create) suppresses the field.
func TestP4_OrgSwitchOffSuppresses(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	patch := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{
		"switches": map[string]any{"mailbox.webSafeHtml": false},
	})
	if patch.Status != http.StatusOK {
		t.Fatalf("settings patch = %d %s", patch.Status, patch.Body)
	}

	user := fmt.Sprintf("p4org%d", time.Now().UnixNano())
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)
	secret := harness.WebhookSecret()
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "org-off", "kind": "http", "url": sk.URL, "events": []string{"message.received"},
		"secret": secret, "payload": map[string]any{"webSafeHtml": true, "text": "html"}, "retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	body := harness.AccountCreateBody(t, sim, user, pass, email, "OrgOff", map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 90*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	raw := htmlScriptMsg(email, email, "Org off", "<p4-orgoff@e2e>", `<p>x</p>`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ev := harness.WaitEvent(t, sk, "message.received", 90*time.Second)
	data := eventData(t, ev)
	if _, ok := data["webSafeHtml"]; ok {
		t.Fatalf("org switch off still emitted webSafeHtml: %s", ev.Body)
	}
}
