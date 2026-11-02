//go:build e2e

package p6

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// TestW73_LegacyAppParity_Gmail runs the legacy app's inbound filter + map (docs/site/content/webhooks/filters.md)
// on a Gmail API account: a message labelled INBOX is delivered with the mapped body; one that only carries
// the SENT label (synced into "[Gmail]/Sent Mail") is skipped by isInbox(). The key is a fake value.
func TestW73_LegacyAppParity_Gmail(t *testing.T) {
	const (
		filter  = `type == "message.received" && isInbox()`
		mapExpr = `{"key": env.webhookKey, "body": {"id": data.messageId, "date": event.time,
 "account": account.externalId != "" ? account.externalId : account.id, "event": "messageNew",
 "headers": data.headers, "emailid": data.messageIdHeader, "subject": data.subject}}`
		fakeKey = "fake-legacyapp-key-gmail-e2e"
	)
	gsim := simgmail.New()
	t.Cleanup(gsim.Close)
	f := startBaseWithAPI(t, wrapGmailAPI(t, gsim.URL()), "")
	stamp := time.Now().UnixNano()
	mailbox := fmt.Sprintf("w73g%d@e2e.test", stamp)
	gsim.EnsureUser(mailbox)

	if r := f.Owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{
		"env": map[string]any{"webhookKey": map[string]any{"value": fakeKey, "secret": true}},
	}); r.Status != http.StatusOK {
		t.Fatalf("env: %d %s", r.Status, r.Body)
	}
	in := harness.StartSink(t, sink.Options{})
	secret := harness.WebhookSecret()
	ep := f.Owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "legacyapp-gmail", "kind": "http", "url": in.URL, "events": []string{"*"}, "secret": secret,
		"retry": map[string]any{"maxAttempts": 3, "backoffBase": "200ms", "backoffJitter": 0.0, "timeout": "3s"},
		"rule":  map[string]any{"filter": filter, "map": mapExpr},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("w73-gmail-%d", stamp), "provider": "gmail", "clientId": "cid", "clientSecret": "csec",
		"serviceKey": serviceAccountJSON(t, gsim.URL()+"/token"), "tokenUrl": gsim.URL() + "/token",
	})
	acc := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody("W73 Gmail", mailbox, "gmail", map[string]any{
		"oauth": map[string]any{"appId": app.Str("id")}, "externalId": "studio-gmail",
	}))
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")

	inSub, sentSub := fmt.Sprintf("w73-gmail-in-%d", stamp), fmt.Sprintf("w73-gmail-sent-%d", stamp)
	// New mail after the initial sync (the baseline is silent), picked up by the 2 s history pull.
	waitAccountConnected(t, f.Owner, accID, 60*time.Second)
	time.Sleep(3 * time.Second)
	gsim.PutMessage(mailbox, rfc822("peer@e2e.test", mailbox, inSub, "hi"), []string{"INBOX", "UNREAD"})
	gsim.PutMessage(mailbox, rfc822(mailbox, "peer@e2e.test", sentSub, "out"), []string{"SENT"})
	msg := waitMsgSubject(t, f.Owner, accID, inSub, 60*time.Second)
	waitMsgSubject(t, f.Owner, accID, sentSub, 60*time.Second)

	var body map[string]any
	t.Cleanup(func() {
		if body == nil { // diagnostics on failure
			st := f.Owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+ep.Str("id")+"/rule/stats", nil)
			dec := f.Owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+ep.Str("id")+"/rule/decisions", nil)
			t.Logf("rule stats: %s | decisions: %s | sink requests: %d", st.Body, dec.Body, len(in.Requests()))
		}
	})
	harness.WaitUntil(t, 60*time.Second, "gmail messageNew", func() bool {
		for _, r := range in.Requests() {
			var m map[string]any
			if json.Unmarshal(r.Body, &m) != nil {
				continue
			}
			if b, _ := m["body"].(map[string]any); b != nil && b["subject"] == inSub {
				harness.VerifyWebhook(t, secret, r)
				if m["key"] != fakeKey {
					t.Fatalf("key = %v", m["key"])
				}
				body = b
				return true
			}
		}
		return false
	})
	if body["id"] != msg["id"] || body["account"] != "studio-gmail" || body["event"] != "messageNew" || body["emailid"] == "" {
		t.Fatalf("body = %v", body)
	}
	if hdrs, _ := body["headers"].(map[string]any); hdrs["subject"] == nil || hdrs["message-id"] == nil {
		t.Fatalf("headers = %v", body["headers"])
	}
	time.Sleep(3 * time.Second)
	for _, r := range in.Requests() {
		var m map[string]any
		if json.Unmarshal(r.Body, &m) == nil {
			if b, _ := m["body"].(map[string]any); b != nil && b["subject"] == sentSub {
				t.Fatalf("Sent Mail message delivered: %s", r.Body)
			}
		}
	}
}
