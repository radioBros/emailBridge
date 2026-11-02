//go:build e2e

package p7

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// G4: free-form account metadata through the HTTP API (create, merge patch, list filter, field errors) and
// into the rules engine (account.metadata in a route filter and map, loaded from the stored account).
func TestG4_AccountMetadataAPIAndRules(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	account := func(ext string, meta map[string]any) map[string]any {
		body := map[string]any{
			"name": "G4 " + ext, "email": ext + "@example.com", "externalId": ext, "skipVerify": true,
			"imap":     map[string]any{"host": "127.0.0.1", "port": 9, "tls": "none", "auth": map[string]any{"user": "u", "pass": "p"}},
			"smtp":     map[string]any{"host": "127.0.0.1", "port": 9, "tls": "none", "auth": map[string]any{"user": "u", "pass": "p"}},
			"switches": map[string]any{"accounts.ignoreCertErrors": false},
		}
		if meta != nil {
			body["metadata"] = meta
		}
		return body
	}

	created := owner.Do(http.MethodPost, "/v1/accounts", account("g4-a", map[string]any{"crm": "x", "crm.id": "48213", "tier": 2}))
	harness.ExpectStatus(t, created, http.StatusCreated)
	accID := created.Str("id")
	meta, _ := created.JSON["metadata"].(map[string]any)
	if meta["crm"] != "x" || meta["crm.id"] != "48213" || meta["tier"] != float64(2) {
		t.Fatalf("create metadata: %s", created.Body)
	}
	other := owner.Do(http.MethodPost, "/v1/accounts", account("g4-b", map[string]any{"crm": "y"}))
	harness.ExpectStatus(t, other, http.StatusCreated)

	// Invalid metadata → 422 validation_failed with a field error on the key.
	bad := owner.Do(http.MethodPost, "/v1/accounts", account("g4-c", map[string]any{"nested": map[string]any{"a": 1}}))
	harness.ExpectStatus(t, bad, http.StatusUnprocessableEntity)
	if bad.Str("code") != "validation_failed" {
		t.Fatalf("bad create: %s", bad.Body)
	}
	items, _ := bad.JSON["errors"].([]any)
	first, _ := firstItem(items)
	if first["path"] != "body.metadata.nested" {
		t.Fatalf("field errors: %s", bad.Body)
	}

	// Merge patch: change tier, remove crm.id, add region; crm stays.
	owner.Hdr = map[string]string{"If-Match": created.Header.Get("ETag")}
	patched := owner.Do(http.MethodPatch, "/v1/accounts/"+accID, map[string]any{
		"metadata": map[string]any{"tier": 3, "crm.id": nil, "region": "IT-MI"},
	})
	owner.Hdr = nil
	harness.ExpectStatus(t, patched, http.StatusOK)
	meta, _ = patched.JSON["metadata"].(map[string]any)
	if len(meta) != 3 || meta["crm"] != "x" || meta["tier"] != float64(3) || meta["region"] != "IT-MI" {
		t.Fatalf("patched metadata: %s", patched.Body)
	}
	badPatch := owner.Do(http.MethodPatch, "/v1/accounts/"+accID, map[string]any{"metadata": "nope"})
	harness.ExpectStatus(t, badPatch, http.StatusUnprocessableEntity)

	// List filter: string equality on a key; more than 3 filters is rejected.
	list := owner.Do(http.MethodGet, "/v1/accounts?metadata.crm=x&metadata.region="+url.QueryEscape("IT-MI"), nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	data, _ := list.JSON["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("filtered list: %s", list.Body)
	}
	if row, _ := data[0].(map[string]any); row["id"] != accID {
		t.Fatalf("filtered list: %s", list.Body)
	}
	tooMany := owner.Do(http.MethodGet, "/v1/accounts?metadata.a=1&metadata.b=1&metadata.c=1&metadata.d=1", nil)
	harness.ExpectStatus(t, tooMany, http.StatusUnprocessableEntity)
	dup := owner.Do(http.MethodGet, "/v1/accounts?metadata.a=1&metadata.a=2", nil)
	harness.ExpectStatus(t, dup, http.StatusUnprocessableEntity)

	// Rules: the route test loads the event's account, so account.metadata is the stored object.
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "g4-ep", "kind": "http", "url": "http://127.0.0.1:9/hook",
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(), "retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	rt := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
		"name": "g4-route", "endpointId": ep.Str("id"),
		"filter": `type == "message.received" && account.metadata.crm == "x"`,
		"map":    `{"crm": account.metadata.crm, "tier": account.metadata.tier, "hasOld": has(account.metadata.old)}`,
	})
	harness.ExpectStatus(t, rt, http.StatusCreated)
	routeID := rt.Str("id")
	testFor := func(id string) harness.Resp {
		return owner.Do(http.MethodPost, "/v1/routes/"+routeID+"/test", map[string]any{
			"event": map[string]any{"type": "message.received", "ebaccount": id, "data": map[string]any{"accountId": id}},
		})
	}
	hit := testFor(accID)
	harness.ExpectStatus(t, hit, http.StatusOK)
	if hit.JSON["matched"] != true {
		t.Fatalf("route test on metadata: %s", hit.Body)
	}
	mapped, _ := hit.JSON["mapped"].(map[string]any)
	if mapped["crm"] != "x" || mapped["tier"] != float64(3) || mapped["hasOld"] != false {
		t.Fatalf("mapped: %s", hit.Body)
	}
	miss := testFor(other.Str("id"))
	harness.ExpectStatus(t, miss, http.StatusOK)
	if miss.JSON["matched"] != false {
		t.Fatalf("other account must not match: %s", miss.Body)
	}
}

func firstItem(items []any) (map[string]any, bool) {
	if len(items) == 0 {
		return nil, false
	}
	m, ok := items[0].(map[string]any)
	return m, ok
}
