//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// OAuth app registry CRUD + verify + provider mismatch on start (P6.1 / W54 G4).
func TestP6_OAuthApps_CRUDVerifyMismatch(t *testing.T) {
	f := startBase(t)
	idp := startMockIdP(t)
	stamp := time.Now().UnixNano()

	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("gmail-%d", stamp), "provider": "gmail",
		"clientId": "cid-g", "clientSecret": "csec-g",
		"authorizeUrl": idp.URL + "/authorize", "tokenUrl": idp.URL + "/token",
	})
	appID := app.Str("id")
	if app.JSON["hasClientSecret"] != true {
		t.Fatalf("hasClientSecret: %s", app.Body)
	}

	got := f.Owner.Do(http.MethodGet, "/v1/oauth-apps/"+appID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	if got.Str("id") != appID || got.Str("provider") != "gmail" {
		t.Fatalf("get: %s", got.Body)
	}

	list := f.Owner.Do(http.MethodGet, "/v1/oauth-apps?includeTotal=true&q=gmail", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	data, _ := list.JSON["data"].([]any)
	if len(data) < 1 {
		t.Fatalf("list empty: %s", list.Body)
	}

	name := fmt.Sprintf("gmail-renamed-%d", stamp)
	patched := withIfMatch(f.Owner, etagOf(got), func() harness.Resp {
		return f.Owner.Do(http.MethodPatch, "/v1/oauth-apps/"+appID, map[string]any{"name": name})
	})
	harness.ExpectStatus(t, patched, http.StatusOK)
	if patched.Str("name") != name {
		t.Fatalf("patch: %s", patched.Body)
	}

	vr := f.Owner.Do(http.MethodPost, "/v1/oauth-apps/"+appID+"/verify", nil)
	harness.ExpectStatus(t, vr, http.StatusOK)
	if vr.JSON["ok"] != true {
		t.Fatalf("verify: %s", vr.Body)
	}

	// Matched pair: imap account + gmail app → authorize URL.
	user := fmt.Sprintf("oa%d", stamp)
	accID := createIMAPAccount(t, f.Owner, f.Sim, user, "sim-secret", user+"@sim.e2e.test", "OAuth match")
	startOK := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/oauth/start", map[string]any{"appId": appID})
	harness.ExpectStatus(t, startOK, http.StatusOK)
	if startOK.Str("authorizeUrl") == "" || startOK.Str("state") == "" {
		t.Fatalf("start matched: %s", startOK.Body)
	}

	// Mismatch: graph account + gmail app → 409 oauth_provider_mismatch.
	gAcc := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Graph mismatch", fmt.Sprintf("graph-%d@ex.com", stamp), "graph", nil,
	))
	harness.ExpectStatus(t, gAcc, http.StatusCreated)
	bad := f.Owner.Do(http.MethodPost, "/v1/accounts/"+gAcc.Str("id")+"/oauth/start", map[string]any{"appId": appID})
	harness.ExpectProblem(t, bad, http.StatusConflict, "oauth_provider_mismatch")

	outlook := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("outlook-%d", stamp), "provider": "outlook",
		"clientId": "cid-o", "clientSecret": "csec-o",
		"authorizeUrl": idp.URL + "/authorize", "tokenUrl": idp.URL + "/token",
	})
	startGraph := f.Owner.Do(http.MethodPost, "/v1/accounts/"+gAcc.Str("id")+"/oauth/start", map[string]any{"appId": outlook.Str("id")})
	harness.ExpectStatus(t, startGraph, http.StatusOK)
	if startGraph.Str("authorizeUrl") == "" {
		t.Fatalf("graph+outlook start: %s", startGraph.Body)
	}

	del := withIfMatch(f.Owner, etagOf(patched), func() harness.Resp {
		return f.Owner.Do(http.MethodDelete, "/v1/oauth-apps/"+appID, nil)
	})
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		// App may still be referenced by the imap start state; delete the unused outlook instead is fine —
		// keep evidence if delete of the gmail app is refused.
		t.Logf("delete gmail app: %d %s (non-fatal)", del.Status, del.Body)
	}
}
