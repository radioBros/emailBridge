//go:build e2e

package p0

import (
	"net/http"
	"testing"
)

func TestKeyProtectedSettings(t *testing.T) {
	n := StartNode(t, nil)
	owner, secret := n.OwnerSession(t)
	_ = secret

	// create API key with settings:read
	r := owner.do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "settings-reader", "scopes": []string{"settings:read"},
	})
	expectStatus(t, r, http.StatusCreated)
	token, _ := r.JSON["token"].(string)
	keyID, _ := r.JSON["id"].(string)
	if token == "" || keyID == "" {
		t.Fatalf("create key body: %s", r.Body)
	}
	etag := r.Header.Get("ETag")

	bearer := n.Client()
	bearer.hdr = map[string]string{"Authorization": "Bearer " + token}
	if r := bearer.do(http.MethodGet, "/v1/settings", nil); r.Status != http.StatusOK {
		t.Fatalf("settings with key = %d %s", r.Status, r.Body)
	}

	anon := n.Client()
	expectProblem(t, anon.do(http.MethodGet, "/v1/settings", nil), http.StatusUnauthorized, "unauthenticated")

	// a key without settings scopes can neither read nor write settings (reads name their scope, S10 §3)
	r = owner.do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "logs-only", "scopes": []string{"logs:read"},
	})
	expectStatus(t, r, http.StatusCreated)
	logsTok, _ := r.JSON["token"].(string)
	logsOnly := n.Client()
	logsOnly.hdr = map[string]string{"Authorization": "Bearer " + logsTok}
	expectProblem(t, logsOnly.do(http.MethodGet, "/v1/settings", nil), http.StatusForbidden, "scope_missing")
	expectProblem(t, logsOnly.do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{"webhookTimeout": "20s"}),
		http.StatusForbidden, "scope_missing")

	// revoke settings key → 401 immediately
	if etag == "" {
		g := owner.do(http.MethodGet, "/v1/api-keys/"+keyID, nil)
		expectStatus(t, g, http.StatusOK)
		etag = g.Header.Get("ETag")
	}
	owner.hdr = map[string]string{"If-Match": etag}
	del := owner.do(http.MethodDelete, "/v1/api-keys/"+keyID, nil)
	owner.hdr = nil
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("revoke = %d %s", del.Status, del.Body)
	}
	r = bearer.do(http.MethodGet, "/v1/settings", nil)
	if r.Status != http.StatusUnauthorized || (r.Str("code") != "api_key_revoked" && r.Str("code") != "api_key_invalid") {
		t.Fatalf("revoked key: want 401 api_key_revoked|api_key_invalid, got %d %s", r.Status, r.Body)
	}
}
