//go:build e2e

package p1

import (
	"net/http"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Every POST accepts Idempotency-Key (S2 §2): a retried create returns the first result and creates nothing.
func TestP1_12_IdempotentCreate(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	body := map[string]any{
		"name": "idem-ep", "kind": "http", "url": "http://127.0.0.1:9/hook", "events": []string{"message.received"},
		"secret": harness.WebhookSecret(), "retry": defaultRetry(),
	}
	owner.Hdr = map[string]string{"Idempotency-Key": "e2e-create-12"}
	first := owner.Do(http.MethodPost, "/v1/webhook-endpoints", body)
	harness.ExpectStatus(t, first, http.StatusCreated)
	again := owner.Do(http.MethodPost, "/v1/webhook-endpoints", body)
	harness.ExpectStatus(t, again, http.StatusCreated)
	if again.Header.Get("Idempotent-Replayed") != "true" || again.Str("id") != first.Str("id") {
		t.Fatalf("replay: header=%q id=%s first=%s", again.Header.Get("Idempotent-Replayed"), again.Str("id"), first.Str("id"))
	}
	body["name"] = "other"
	harness.ExpectProblem(t, owner.Do(http.MethodPost, "/v1/webhook-endpoints", body), http.StatusConflict, "idempotency_key_reused")

	// A replayed API-key create confirms the key but never shows its one-time token again.
	owner.Hdr = map[string]string{"Idempotency-Key": "e2e-key-12"}
	keyBody := map[string]any{"name": "idem-key", "scopes": []string{"accounts:read"}}
	k1 := owner.Do(http.MethodPost, "/v1/api-keys", keyBody)
	harness.ExpectStatus(t, k1, http.StatusCreated)
	k2 := owner.Do(http.MethodPost, "/v1/api-keys", keyBody)
	harness.ExpectStatus(t, k2, http.StatusCreated)
	if k1.Str("token") == "" || k2.Str("token") != "[redacted]" || k2.Str("id") != k1.Str("id") {
		t.Fatalf("api key replay: first token set=%v replay token=%q ids %s/%s", k1.Str("token") != "", k2.Str("token"), k1.Str("id"), k2.Str("id"))
	}

	owner.Hdr = nil
	list := owner.Do(http.MethodGet, "/v1/webhook-endpoints?page=1&includeTotal=true", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	if total, _ := list.JSON["total"].(float64); total != 1 {
		t.Fatalf("endpoints created: %v (%s)", list.JSON["total"], list.Body)
	}
}
