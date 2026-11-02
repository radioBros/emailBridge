//go:build e2e

package p7

import (
	"net/http"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func TestW73_EndpointRule_IsInboxTraceAndMap(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "w73-inbox-ep", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(),
		"retry": defaultRetry(),
		"rule": map[string]any{
			"filter": `type == "message.received" && isInbox()`,
			"map":    `{"from": from(), "inbox": true}`,
		},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")

	ok := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+epID+"/rule/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{
				"mailbox": map[string]any{"path": "INBOX"},
				"from":    map[string]any{"address": "a@example.com"},
			},
		},
	})
	harness.ExpectStatus(t, ok, http.StatusOK)
	if ok.JSON["matched"] != true {
		t.Fatalf("want matched: %s", ok.Body)
	}
	trace, _ := ok.JSON["trace"].([]any)
	if len(trace) < 2 {
		t.Fatalf("want trace terms: %s", ok.Body)
	}
	mapped, _ := ok.JSON["mapped"].(map[string]any)
	if mapped["from"] != "a@example.com" || mapped["inbox"] != true {
		t.Fatalf("mapped: %#v", mapped)
	}

	gmail := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+epID+"/rule/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{
				"mailbox": map[string]any{"path": "[Gmail]/All Mail"},
				"labels":  []string{`\Inbox`},
				"from":    map[string]any{"address": "b@example.com"},
			},
		},
	})
	harness.ExpectStatus(t, gmail, http.StatusOK)
	if gmail.JSON["matched"] != true {
		t.Fatalf("gmail inbox: %s", gmail.Body)
	}

	miss := owner.Do(http.MethodPost, "/v1/webhook-endpoints/"+epID+"/rule/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{"mailbox": map[string]any{"path": "Sent"}},
		},
	})
	harness.ExpectStatus(t, miss, http.StatusOK)
	if miss.JSON["matched"] != false {
		t.Fatalf("want unmatched: %s", miss.Body)
	}
}

func TestW73_NamedListAndAttachmentBody(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	list := owner.Do(http.MethodPost, "/v1/rule-lists", map[string]any{
		"name": "vip", "kind": "domains", "entries": []string{"partner.com"},
	})
	harness.ExpectStatus(t, list, http.StatusCreated)

	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "w73-list-ep", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(),
		"retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	rt := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name":       "w73-list-route",
		"endpointId": ep.Str("id"),
		"filter":     `type == "message.received" && inList("vip", fromDomain()) && hasAttachment("*.pdf") && textContains("invoice")`,
		"map":        `pick({"from": from(), "ok": true}, ["from", "ok"])`,
	})
	harness.ExpectStatus(t, rt, http.StatusCreated)
	routeID := rt.Str("id")

	hit := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{
				"from":        map[string]any{"address": "x@partner.com"},
				"text":        map[string]any{"plain": "Please see invoice"},
				"attachments": []any{map[string]any{"filename": "inv.pdf", "contentType": "application/pdf", "size": 10}},
			},
		},
	})
	harness.ExpectStatus(t, hit, http.StatusOK)
	if hit.JSON["matched"] != true {
		t.Fatalf("want match: %s", hit.Body)
	}

	get := owner.Do(http.MethodGet, "/v1/rule-lists/"+list.Str("id"), nil)
	harness.ExpectStatus(t, get, http.StatusOK)
	if owner.Hdr == nil {
		owner.Hdr = map[string]string{}
	}
	owner.Hdr["If-Match"] = get.Header.Get("ETag")
	patch := owner.Do(http.MethodPatch, "/v1/rule-lists/"+list.Str("id"), map[string]any{
		"entries": []string{"other.com"},
	})
	delete(owner.Hdr, "If-Match")
	harness.ExpectStatus(t, patch, http.StatusOK)

	miss := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{
				"from":        map[string]any{"address": "x@partner.com"},
				"text":        map[string]any{"plain": "Please see invoice"},
				"attachments": []any{map[string]any{"filename": "inv.pdf", "contentType": "application/pdf", "size": 10}},
			},
		},
	})
	harness.ExpectStatus(t, miss, http.StatusOK)
	if miss.JSON["matched"] != false {
		t.Fatalf("list change should miss: %s", miss.Body)
	}
}
