//go:build e2e

package p8

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Catalogue lists connectors; install → test → uninstall Slack (chat) through the public API.
func TestP8_CatalogueInstallTestUninstall(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	cat := owner.Do(http.MethodGet, "/v1/integrations", nil)
	harness.ExpectStatus(t, cat, http.StatusOK)
	data, _ := cat.JSON["data"].([]any)
	if len(data) < 5 {
		t.Fatalf("catalogue too small: %s", cat.Body)
	}
	ids := map[string]bool{}
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		ids[id] = true
	}
	for _, want := range []string{"slack", "hubspot", "pipedrive", "gdrive", "onedrive", "ses", "featherdesk", "sentry"} {
		if !ids[want] {
			t.Fatalf("catalogue missing %s: %v", want, ids)
		}
	}

	sk := harness.StartSink(t, sink.Options{})
	inst := install(t, owner, "slack", map[string]any{
		"name":   "p8-slack",
		"config": map[string]any{"webhookUrl": sk.URL},
	})
	id := inst.Str("id")
	if !inst.JSON["enabled"].(bool) {
		t.Fatal("expected enabled")
	}

	tr := owner.Do(http.MethodPost, "/v1/integration-instances/"+id+"/test", map[string]any{})
	harness.ExpectStatus(t, tr, http.StatusOK)
	if ok, _ := tr.JSON["ok"].(bool); !ok {
		t.Fatalf("test not ok: %s", tr.Body)
	}
	harness.WaitUntil(t, 15*time.Second, "slack test POST", func() bool {
		return len(sk.Requests()) >= 1
	})

	del := owner.Do(http.MethodDelete, "/v1/integration-instances/"+id, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		// retry with If-Match when required
		prev := owner.Hdr
		owner.Hdr = map[string]string{"If-Match": etag(inst.JSON["version"])}
		del = owner.Do(http.MethodDelete, "/v1/integration-instances/"+id, nil)
		owner.Hdr = prev
	}
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("uninstall = %d %s", del.Status, del.Body)
	}
	gone := owner.Do(http.MethodGet, "/v1/integration-instances/"+id, nil)
	if gone.Status != http.StatusNotFound {
		t.Fatalf("after uninstall get = %d %s", gone.Status, gone.Body)
	}
}

// Organisation B cannot read, test, patch or uninstall organisation A's instance.
func TestP8_TenantIsolationInstance(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	sk := harness.StartSink(t, sink.Options{})
	inst := install(t, owner, "slack", map[string]any{
		"name":   "org-a-slack",
		"config": map[string]any{"webhookUrl": sk.URL},
	})
	id := inst.Str("id")

	b, _ := orgBSession(t, n, owner)
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/integration-instances/" + id, nil},
		{http.MethodPost, "/v1/integration-instances/" + id + "/test", map[string]any{}},
		{http.MethodPatch, "/v1/integration-instances/" + id, map[string]any{"name": "hijack"}},
		{http.MethodDelete, "/v1/integration-instances/" + id, nil},
	} {
		r := b.Do(op.method, op.path, op.body)
		if r.Status < 400 {
			t.Fatalf("org B %s %s → %d %s", op.method, op.path, r.Status, r.Body)
		}
		code := r.Str("code")
		if code != "not_found" && code != "forbidden" && code != "organization_mismatch" && code != "unauthenticated" {
			// still a refusal — surface the body for triage
			if r.Status == http.StatusNotFound || r.Status == http.StatusForbidden {
				continue
			}
			t.Fatalf("org B %s %s → %d code=%s %s", op.method, op.path, r.Status, code, r.Body)
		}
	}

	list := b.Do(http.MethodGet, "/v1/integration-instances?includeTotal=true", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	if strings.Contains(string(list.Body), id) {
		t.Fatalf("org B list leaked org A instance %s: %s", id, list.Body)
	}
}
