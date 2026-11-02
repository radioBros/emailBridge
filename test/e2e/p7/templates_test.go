//go:build e2e

package p7

import (
	"net/http"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func TestP7_Templates_CRUDPreviewAndFeatureGate(t *testing.T) {
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	created := owner.Do(http.MethodPost, "/v1/templates", map[string]any{
		"name":   "p7-welcome",
		"text":   "Hello {{name}}",
		"html":   "<p>Hello {{name}}</p>",
		"strict": true,
	})
	harness.ExpectStatus(t, created, http.StatusCreated)
	tplID := created.Str("id")
	if tplID == "" {
		t.Fatalf("create: %s", created.Body)
	}

	prev := owner.Do(http.MethodPost, "/v1/templates/"+tplID+"/preview", map[string]any{
		"variables": map[string]any{"name": "Ada"},
	})
	harness.ExpectStatus(t, prev, http.StatusOK)
	if prev.Str("text") != "Hello Ada" {
		t.Fatalf("preview text: %s", prev.Body)
	}

	got := owner.Do(http.MethodGet, "/v1/templates/"+tplID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	if got.Str("name") != "p7-welcome" {
		t.Fatalf("get: %s", got.Body)
	}

	list := owner.Do(http.MethodGet, "/v1/templates?q=welcome&limit=10&includeTotal=true", nil)
	harness.ExpectStatus(t, list, http.StatusOK)

	// Switch off → create / preview gated.
	enableOrgSwitch(t, owner, "sending.templates", false)
	blocked := owner.Do(http.MethodPost, "/v1/templates", map[string]any{
		"name": "p7-blocked", "text": "x",
	})
	if blocked.Status != http.StatusForbidden || problemCode(blocked) != "feature_disabled" {
		t.Fatalf("want 403 feature_disabled, got %d %s", blocked.Status, blocked.Body)
	}
	enableOrgSwitch(t, owner, "sending.templates", true)

	del := owner.Do(http.MethodDelete, "/v1/templates/"+tplID, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Status, del.Body)
	}
}
