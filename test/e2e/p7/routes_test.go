//go:build e2e

package p7

import (
	"net/http"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func TestP7_Routes_CELFilterMapAndTest(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "p7-route-ep", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(),
		"retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")

	comp := owner.Do(http.MethodPost, "/v1/routes/builder/compile", map[string]any{
		"builder": map[string]any{
			"match": "all",
			"conditions": []map[string]any{
				{"field": "type", "op": "in", "value": []string{"message.received"}},
				{"field": "subject", "op": "contains", "value": "invoice"},
			},
		},
	})
	harness.ExpectStatus(t, comp, http.StatusOK)
	if cel, _ := comp.JSON["cel"].(string); cel == "" || comp.JSON["ok"] != true {
		t.Fatalf("compile: %s", comp.Body)
	}

	rt := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name":       "p7-cel-route",
		"endpointId": epID,
		"filter":     `type == "message.received" && data.subject.contains("invoice")`,
		"map":        `{"subject": data.subject, "ok": true}`,
	})
	harness.ExpectStatus(t, rt, http.StatusCreated)
	routeID := rt.Str("id")
	if routeID == "" {
		t.Fatalf("create route: %s", rt.Body)
	}

	testOK := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{"subject": "Your invoice #42"},
		},
	})
	harness.ExpectStatus(t, testOK, http.StatusOK)
	if testOK.JSON["matched"] != true {
		t.Fatalf("want matched: %s", testOK.Body)
	}
	mapped, _ := testOK.JSON["mapped"].(map[string]any)
	if mapped == nil || mapped["subject"] != "Your invoice #42" || mapped["ok"] != true {
		t.Fatalf("mapped fields: %#v body=%s", mapped, testOK.Body)
	}

	miss := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{"subject": "hello"},
		},
	})
	harness.ExpectStatus(t, miss, http.StatusOK)
	if miss.JSON["matched"] != false {
		t.Fatalf("want unmatched: %s", miss.Body)
	}
}

func TestP7_Routes_TestMasksSecretEnv(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	secretVal := "super-secret-env-value-9f3a"
	patchOrgEnv(t, owner, map[string]any{"webhookKey": map[string]any{"value": secretVal, "secret": true}})

	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "p7-mask-ep", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(),
		"retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	rt := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name":       "p7-mask-route",
		"endpointId": ep.Str("id"),
		"filter":     `type == "message.received"`,
		"map":        `{"key": env.webhookKey}`,
	})
	harness.ExpectStatus(t, rt, http.StatusCreated)
	routeID := rt.Str("id")

	masked := owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
		"event": map[string]any{
			"type": "message.received",
			"data": map[string]any{"subject": "x"},
		},
	})
	harness.ExpectStatus(t, masked, http.StatusOK)
	if errStr, _ := masked.JSON["error"].(string); strings.Contains(errStr, "no such key") {
		t.Fatalf("settings.env never reaches CEL env.*: %s", masked.Body)
	}
	if strings.Contains(string(masked.Body), secretVal) {
		t.Fatalf("BUG: secret env value leaked in route test (want ••••). body=%s", masked.Body)
	}
	mapped, _ := masked.JSON["mapped"].(map[string]any)
	if mapped == nil {
		t.Fatalf("mapped missing: %s", masked.Body)
	}
	key, _ := mapped["key"].(string)
	if key != "••••" {
		t.Fatalf("want masked key ••••, got %q body=%s", key, masked.Body)
	}
}
