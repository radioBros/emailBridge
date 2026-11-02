//go:build e2e

package p7

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// the legacy app's two EmailEngine webhooks, translated (docs/site/content/webhooks/filters.md). The key is a
// secret in settings.env; this test uses a fake value only.
const (
	parityInboundFilter = `type == "message.received" && isInbox()`
	parityInboundMap    = `{
  "key": env.webhookKey,
  "body": {
    "id": data.messageId,
    "date": event.time,
    "account": account.externalId != "" ? account.externalId : account.id,
    "event": "messageNew",
    "headers": data.headers,
    "emailid": data.messageIdHeader,
    "subject": data.subject
  }
}`
	parityOutboundMap = `{"key": env.webhookKey, "body": event}`
	parityFakeKey     = "fake-legacyapp-key-e2e"
	parityExternalID  = "legacyapp-studio-1"
)

var parityOutboundEvents = []string{"message.sent", "message.failed", "message.delivery_attempt_failed"}

type parityDelivery struct {
	Type string
	Body map[string]any
}

func parityBodies(t *testing.T, s *sink.Server, secret string) []parityDelivery {
	t.Helper()
	var out []parityDelivery
	for _, r := range s.Requests() {
		harness.VerifyWebhook(t, secret, r)
		var m map[string]any
		if err := json.Unmarshal(r.Body, &m); err != nil {
			t.Fatalf("non-JSON delivery: %s", r.Body)
		}
		out = append(out, parityDelivery{Type: harness.EventType(r), Body: m})
	}
	return out
}

func TestW73_LegacyAppParity_InboundAndOutbound(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, p7Env(map[string]string{
		"EB_SUBMIT_BACKOFF_BASE": "3s", "EB_SYNC_NOIDLE_POLL": "3s", "EB_SUBMIT_BACKOFF_JITTER": "0", "EB_SUBMIT_MAX_ATTEMPTS": "6",
	}))
	owner, _ := n.OwnerSession(t)
	patchOrgEnv(t, owner, map[string]any{"webhookKey": map[string]any{"value": parityFakeKey, "secret": true}})

	// Endpoints first: the payload-needs cache must see data.headers (all headers) before the first sync.
	inSink := harness.StartSink(t, sink.Options{})
	outSink := harness.StartSink(t, sink.Options{})
	inSecret, outSecret := harness.WebhookSecret(), harness.WebhookSecret()
	in := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "legacyapp-inbound", "kind": "http", "url": inSink.URL,
		"events": []string{"*"}, "secret": inSecret, "retry": defaultRetry(),
		"rule": map[string]any{"filter": parityInboundFilter, "map": parityInboundMap},
	})
	harness.ExpectStatus(t, in, http.StatusCreated)
	out := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "legacyapp-outbound", "kind": "http", "url": outSink.URL,
		"events": parityOutboundEvents, "secret": outSecret, "retry": defaultRetry(),
		"rule": map[string]any{"map": parityOutboundMap},
	})
	harness.ExpectStatus(t, out, http.StatusCreated)
	if rule, _ := in.JSON["rule"].(map[string]any); rule == nil || rule["currentVersion"] != float64(1) {
		t.Fatalf("inbound rule: %s", in.Body)
	}

	user := fmt.Sprintf("w73p%d", time.Now().UnixNano())
	pass, email := "sim-secret", ""
	email = user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := sim.Client.CreateMailbox(ctx, user, "Projects"); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	body := harness.AccountCreateBody(t, sim, user, pass, email, "LegacyApp parity", map[string]any{"sandbox.enabled": false}, "standard")
	body["externalId"] = parityExternalID
	body["sync"] = map[string]any{"paths": []string{"*"}, "idlePaths": []string{"INBOX", "Projects"}}
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	// --- Inbound: an Inbox message is delivered with the mapped body; a message in another folder is not.
	stamp := time.Now().UnixNano()
	inSubject, otherSubject := fmt.Sprintf("parity-in-%d", stamp), fmt.Sprintf("parity-other-%d", stamp)
	inMID := fmt.Sprintf("<parity-in-%d@e2e.test>", stamp)
	sim.InjectRaw(t, user, "INBOX", harness.RawMessage("peer@e2e.test", email, inSubject, inMID, "hello"), nil)
	sim.InjectRaw(t, user, "Projects", harness.RawMessage("peer@e2e.test", email, otherSubject, "", "elsewhere"), nil)
	msgID := waitMessageID(t, owner, accID, inSubject, 60*time.Second)

	var got map[string]any
	harness.WaitUntil(t, 60*time.Second, "inbound messageNew", func() bool {
		for _, d := range parityBodies(t, inSink, inSecret) {
			if b, _ := d.Body["body"].(map[string]any); b != nil && b["subject"] == inSubject {
				got = d.Body
				return true
			}
		}
		return false
	})
	if len(got) != 2 || got["key"] != parityFakeKey {
		t.Fatalf("delivered top level = %v", got)
	}
	b := got["body"].(map[string]any)
	if len(b) != 7 {
		t.Fatalf("body fields = %v", b)
	}
	if b["id"] != msgID || b["account"] != parityExternalID || b["event"] != "messageNew" ||
		b["emailid"] != inMID || b["subject"] != inSubject {
		t.Fatalf("body = %v", b)
	}
	if ts, _ := b["date"].(string); ts == "" {
		t.Fatalf("date = %v", b["date"])
	} else if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("date %q: %v", ts, err)
	}
	headers, _ := b["headers"].(map[string]any)
	for _, h := range []string{"from", "to", "subject", "message-id", "date", "mime-version", "content-type"} {
		vals, _ := headers[h].([]any)
		if len(vals) == 0 {
			t.Fatalf("all headers must be delivered (payload needs *): missing %q in %v", h, headers)
		}
	}
	for k := range headers {
		if k != strings.ToLower(k) {
			t.Fatalf("header %q not lower-cased", k)
		}
	}
	if vals := headers["message-id"].([]any); vals[0] != inMID {
		t.Fatalf("message-id header = %v", vals)
	}

	// The Projects message is synced but skipped by isInbox(): wait until it is indexed and its event
	// evaluated, then make sure nothing was delivered for it.
	// Folders other than the IDLE one sync on the resync timer; ask for a pass now.
	if r := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/sync", map[string]any{"mode": "full"}); r.Status != http.StatusAccepted && r.Status != http.StatusOK {
		t.Fatalf("sync nudge: %d %s", r.Status, r.Body)
	}
	waitMessageID(t, owner, accID, otherSubject, 90*time.Second)
	var otherEventID string
	harness.WaitUntil(t, 30*time.Second, "other-folder event in the test-event picker", func() bool {
		r := owner.Do(http.MethodGet, "/v1/routes/test-events?type=message.received&accountId="+accID, nil)
		rows, _ := r.JSON["data"].([]any)
		for _, raw := range rows {
			if m, _ := raw.(map[string]any); m != nil && m["subject"] == otherSubject {
				otherEventID, _ = m["id"].(string)
			}
		}
		return otherEventID != ""
	})
	harness.WaitUntil(t, 30*time.Second, "other-folder decision", func() bool {
		r := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+in.Str("id")+"/rule/decisions", nil)
		rows, _ := r.JSON["data"].([]any)
		for _, raw := range rows {
			if m, _ := raw.(map[string]any); m != nil && m["eventId"] == otherEventID {
				return m["matched"] == false
			}
		}
		return false
	})
	stats := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+in.Str("id")+"/rule/stats", nil)
	harness.ExpectStatus(t, stats, http.StatusOK)
	if m, _ := stats.JSON["matched"].(float64); m < 1 {
		t.Fatalf("stats: %s", stats.Body)
	}
	time.Sleep(2 * time.Second)
	for _, d := range parityBodies(t, inSink, inSecret) {
		if b, _ := d.Body["body"].(map[string]any); b != nil && b["subject"] == otherSubject {
			t.Fatalf("message outside the Inbox delivered: %v", d.Body)
		}
	}

	// --- Outbound status: ok, temporary failure then ok, permanent failure.
	submit := func(subject string) string {
		r := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
			"to": []map[string]any{{"address": email}}, "subject": subject, "text": "status " + subject,
		})
		harness.ExpectStatus(t, r, http.StatusAccepted)
		return r.Str("id")
	}
	setScript := func(script simulator.SMTPScript) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		if script == nil {
			err = sim.Client.ClearSMTPScript(ctx, user)
		} else {
			err = sim.Client.SetSMTPScript(ctx, user, script)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	typesFor := func(outID string) []parityDelivery {
		var rows []parityDelivery
		for _, d := range parityBodies(t, outSink, outSecret) {
			ev, _ := d.Body["body"].(map[string]any)
			data, _ := ev["data"].(map[string]any)
			if data != nil && data["outboundId"] == outID {
				rows = append(rows, d)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			ti, _ := rows[i].Body["body"].(map[string]any)["time"].(string)
			tj, _ := rows[j].Body["body"].(map[string]any)["time"].(string)
			return ti < tj
		})
		return rows
	}
	waitType := func(outID, typ string) {
		harness.WaitUntil(t, 90*time.Second, outID+" "+typ, func() bool {
			for _, d := range typesFor(outID) {
				if d.Type == typ {
					return true
				}
			}
			return false
		})
	}

	okID := submit(fmt.Sprintf("parity-ok-%d", stamp))
	owner.WaitOutboundState(t, okID, "sent", 90*time.Second)
	waitType(okID, "message.sent")

	setScript(simulator.SMTPScript{"rcptTo": {Code: 451, Enhanced: "4.2.0", Text: "try later"}})
	tempID := submit(fmt.Sprintf("parity-temp-%d", stamp))
	waitType(tempID, "message.delivery_attempt_failed")
	setScript(nil)
	owner.WaitOutboundState(t, tempID, "sent", 120*time.Second)
	waitType(tempID, "message.sent")

	setScript(simulator.SMTPScript{"rcptTo": {Code: 550, Enhanced: "5.1.1", Text: "no such user"}})
	permID := submit(fmt.Sprintf("parity-perm-%d", stamp))
	owner.WaitOutboundState(t, permID, "failed", 120*time.Second)
	waitType(permID, "message.failed")
	setScript(nil)
	time.Sleep(2 * time.Second) // let any stray delivery land before the exact-sequence checks

	check := func(outID string, want func([]string) bool) {
		rows := typesFor(outID)
		seq := make([]string, len(rows))
		for i, d := range rows {
			seq[i] = d.Type
			ev, _ := d.Body["body"].(map[string]any)
			if d.Body["key"] != parityFakeKey || len(d.Body) != 2 || ev["type"] != d.Type || ev["specversion"] != "1.0" ||
				ev["id"] == "" || ev["data"] == nil {
				t.Fatalf("%s delivery = %v", outID, d.Body)
			}
			data := ev["data"].(map[string]any)
			if data["accountId"] != accID || data["externalId"] != parityExternalID {
				t.Fatalf("%s data account = %v / %v", outID, data["accountId"], data["externalId"])
			}
		}
		if !want(seq) {
			t.Fatalf("%s sequence = %v", outID, seq)
		}
	}
	check(okID, func(s []string) bool { return len(s) == 1 && s[0] == "message.sent" })
	check(tempID, func(s []string) bool {
		if len(s) < 2 || s[len(s)-1] != "message.sent" {
			return false
		}
		for _, x := range s[:len(s)-1] {
			if x != "message.delivery_attempt_failed" {
				return false
			}
		}
		return true
	})
	check(permID, func(s []string) bool {
		if len(s) == 0 || s[len(s)-1] != "message.failed" {
			return false
		}
		for _, x := range s[:len(s)-1] {
			if x != "message.delivery_attempt_failed" {
				return false
			}
		}
		return true
	})
	// Only the three outbound messages' status events reached the outbound endpoint.
	for _, d := range parityBodies(t, outSink, outSecret) {
		ev, _ := d.Body["body"].(map[string]any)
		data, _ := ev["data"].(map[string]any)
		if id := data["outboundId"]; id != okID && id != tempID && id != permID {
			t.Fatalf("unexpected outbound delivery: %v", d.Body)
		}
	}
	// The inbound endpoint received none of the outbound status events (only messageNew bodies).
	for _, d := range parityBodies(t, inSink, inSecret) {
		for _, typ := range parityOutboundEvents {
			if d.Type == typ {
				t.Fatalf("inbound endpoint got %s: %v", typ, d.Body)
			}
		}
		if b, _ := d.Body["body"].(map[string]any); b == nil || b["event"] != "messageNew" || d.Body["key"] != parityFakeKey {
			t.Fatalf("inbound endpoint got a non-messageNew body: %v", d.Body)
		}
	}
}

// The endpoint-rule management APIs: versions, restore, dry-run, decisions, test with a sample.
func TestW73_EndpointRuleVersionsRestoreDryRun(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "w73-versions", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(), "retry": defaultRetry(),
		"rule": map[string]any{"filter": `isInbox()`},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	id := ep.Str("id")
	etag := ep.Header.Get("ETag")

	owner.Hdr = map[string]string{"If-Match": etag}
	patched := owner.Do(http.MethodPatch, "/v1/webhook-endpoints/"+id, map[string]any{
		"rule": map[string]any{"filter": `isInbox() && hasAttachments()`},
	})
	owner.Hdr = nil
	harness.ExpectStatus(t, patched, http.StatusOK)

	vers := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+id+"/rule/versions?includeTotal=true", nil)
	harness.ExpectStatus(t, vers, http.StatusOK)
	data, _ := vers.JSON["data"].([]any)
	if len(data) != 2 || vers.JSON["total"] != float64(2) {
		t.Fatalf("versions: %s", vers.Body)
	}
	if first, _ := data[0].(map[string]any); first["version"] != float64(2) {
		t.Fatalf("newest first: %s", vers.Body)
	}

	owner.Hdr = map[string]string{"If-Match": patched.Header.Get("ETag")}
	restored := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+id+"/rule/versions/1/restore", nil)
	owner.Hdr = nil
	harness.ExpectStatus(t, restored, http.StatusOK)
	rule, _ := restored.JSON["rule"].(map[string]any)
	if rule["filter"] != `isInbox()` || rule["currentVersion"] != float64(3) {
		t.Fatalf("restore: %s", restored.Body)
	}
	missing := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+id+"/rule/versions/42/restore", nil)
	harness.ExpectStatus(t, missing, http.StatusNotFound)

	dry := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+id+"/rule/dry-run", map[string]any{"windowHours": 1, "limit": 50})
	harness.ExpectStatus(t, dry, http.StatusAccepted)
	job, _ := dry.JSON["job"].(map[string]any)
	jobID, _ := job["id"].(string)
	if jobID == "" || !strings.HasPrefix(dry.Header.Get("Location"), "/v1/jobs/") {
		t.Fatalf("dry-run: %s", dry.Body)
	}
	done := owner.WaitJSON(t, "/v1/jobs/"+jobID, 60*time.Second, "dry-run job", func(r harness.Resp) bool {
		st := r.Str("state")
		return r.Status == http.StatusOK && (st == "succeeded" || st == "failed")
	})
	result, _ := done.JSON["result"].(map[string]any)
	if done.Str("state") != "succeeded" || result == nil || result["scanned"] == nil || result["matched"] == nil {
		t.Fatalf("dry-run job: %s", done.Body)
	}

	test := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+id+"/rule/test", map[string]any{"sample": "message.received"})
	harness.ExpectStatus(t, test, http.StatusOK)
	if test.JSON["matched"] != true {
		t.Fatalf("sample test: %s", test.Body)
	}
	bad := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+id+"/rule/test", map[string]any{"sample": "no.such.type"})
	harness.ExpectStatus(t, bad, http.StatusUnprocessableEntity)

	dec := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+id+"/rule/decisions", nil)
	harness.ExpectStatus(t, dec, http.StatusOK)
	stats := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+id+"/rule/stats", nil)
	harness.ExpectStatus(t, stats, http.StatusOK)
	picker := owner.Do(http.MethodGet, "/v1/routes/test-events?type=message.received", nil)
	harness.ExpectStatus(t, picker, http.StatusOK)
}
