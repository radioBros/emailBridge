//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Account OAuth view, token export (switch / permission / Cache-Control / audit / rate), signatures.
func TestP6_OAuthView_TokenExport_Signatures(t *testing.T) {
	f := startBase(t)
	idp := startMockIdP(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("ox%d", stamp)
	email := user + "@sim.e2e.test"
	accID := createIMAPAccount(t, f.Owner, f.Sim, user, "sim-secret", email, "OAuth view")

	// Not connected → {connected:false}.
	empty := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth", nil)
	harness.ExpectStatus(t, empty, http.StatusOK)
	if empty.JSON["connected"] != false {
		t.Fatalf("empty view: %s", empty.Body)
	}

	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("gen-%d", stamp), "provider": "generic",
		"clientId": "cid", "clientSecret": "csec",
		"authorizeUrl": idp.URL + "/authorize", "tokenUrl": idp.URL + "/token",
		"scopes": []string{"mail"},
	})
	completeOAuth(t, f.Node, f.Owner, accID, app.Str("id"), "generic")

	view := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth", nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	if view.JSON["connected"] != true || view.Str("appId") != app.Str("id") {
		t.Fatalf("connected view: %s", view.Body)
	}
	raw := string(view.Body)
	if strings.Contains(raw, "accessToken") || strings.Contains(raw, "refreshToken") || strings.Contains(raw, "e2e-at-") {
		t.Fatalf("tokens leaked in oauth view: %s", raw)
	}

	// Cross-tenant account id → 404.
	b, _ := orgBSession(t, f.Node, f.Owner)
	cross := b.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth", nil)
	harness.ExpectProblem(t, cross, http.StatusNotFound, "not_found")

	// Token export off → 403 feature_disabled.
	off := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth-token", nil)
	if off.Status != http.StatusForbidden || (problemCode(off) != "feature_disabled" && problemCode(off) != "forbidden") {
		t.Fatalf("export off: %d %s", off.Status, off.Body)
	}

	enableOrgSwitch(t, f.Owner, "oauth.tokenExport", true)

	// Wrong scope (accounts:write only) → 403.
	_, tok := f.Owner.CreateAPIKey(t, "no-export", []string{"accounts:write", "accounts:read"})
	keyCli := f.Node.WithBearer(tok)
	denied := keyCli.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth-token", nil)
	if denied.Status != http.StatusForbidden {
		t.Fatalf("wrong scope: %d %s", denied.Status, denied.Body)
	}

	// Owner → 200 + Cache-Control: no-store.
	exp := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth-token", nil)
	harness.ExpectStatus(t, exp, http.StatusOK)
	if exp.Str("accessToken") == "" || exp.Str("tokenType") != "Bearer" {
		t.Fatalf("export: %s", exp.Body)
	}
	if cc := exp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control=%q body=%s", cc, exp.Body)
	}

	// Audit row.
	aud := f.Owner.WaitJSON(t, "/v1/audit-log?action=account.oauth.token_export&limit=20", 15*time.Second, "token_export audit", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			if m["action"] == "account.oauth.token_export" {
				return true
			}
			if tgt, _ := m["target"].(map[string]any); tgt != nil && tgt["id"] == accID {
				return true
			}
		}
		return false
	})
	if aud.Status != http.StatusOK {
		t.Fatalf("audit: %s", aud.Body)
	}

	// Rate limit (30/hour) → 429.
	hit429 := false
	for i := 0; i < 35; i++ {
		r := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth-token", nil)
		if r.Status == http.StatusTooManyRequests || problemCode(r) == "rate_limited" {
			hit429 = true
			break
		}
		if r.Status != http.StatusOK {
			t.Fatalf("export loop %d: %d %s", i, r.Status, r.Body)
		}
	}
	if !hit429 {
		t.Fatal("expected 429 rate_limited after >30 token exports")
	}

	// Server signatures: IMAP without Gmail OAuth → 400 unsupported_for_provider.
	imapSig := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/server-signatures", nil)
	if imapSig.Status != http.StatusBadRequest && problemCode(imapSig) != "unsupported_for_provider" {
		// generic oauth on imap is not gmail — must refuse
		harness.ExpectProblem(t, imapSig, http.StatusBadRequest, "unsupported_for_provider")
	}

	// Graph → empty + reason (no live Graph call).
	gAcc := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Graph sigs", fmt.Sprintf("gsig-%d@ex.com", stamp), "graph", nil,
	))
	harness.ExpectStatus(t, gAcc, http.StatusCreated)
	gs := f.Owner.Do(http.MethodGet, "/v1/accounts/"+gAcc.Str("id")+"/server-signatures", nil)
	harness.ExpectStatus(t, gs, http.StatusOK)
	if gs.Str("reason") != "graph_signatures_unavailable" {
		t.Fatalf("graph signatures: %s", gs.Body)
	}
	if data, _ := gs.JSON["data"].([]any); data == nil {
		// empty array ok; null not
		if gs.JSON["data"] != nil {
			t.Fatalf("graph data: %s", gs.Body)
		}
	}

	t.Run("gmail_signatures", func(t *testing.T) {
		gsim := simgmail.New()
		t.Cleanup(gsim.Close)
		apiRoot := wrapGmailAPI(t, gsim.URL())
		gf := startBaseWithAPI(t, apiRoot, "")
		gstamp := time.Now().UnixNano()
		gemail := fmt.Sprintf("gsig%d@e2e.test", gstamp)
		gsim.EnsureUser(gemail)
		sk := serviceAccountJSON(t, gsim.URL()+"/token")
		gapp := createOAuthApp(t, gf.Owner, map[string]any{
			"name": fmt.Sprintf("gmail-sig-%d", gstamp), "provider": "gmail",
			"clientId": "cid", "clientSecret": "csec",
			"serviceKey": sk, "tokenUrl": gsim.URL() + "/token",
		})
		gacc := gf.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
			"Gmail sigs", gemail, "gmail", map[string]any{
				"oauth": map[string]any{"appId": gapp.Str("id")},
			},
		))
		harness.ExpectStatus(t, gacc, http.StatusCreated)
		sigs := gf.Owner.Do(http.MethodGet, "/v1/accounts/"+gacc.Str("id")+"/server-signatures", nil)
		harness.ExpectStatus(t, sigs, http.StatusOK)
		data, _ := sigs.JSON["data"].([]any)
		if len(data) == 0 {
			t.Fatalf("gmail signatures empty: %s", sigs.Body)
		}
		first, _ := data[0].(map[string]any)
		if first == nil || fmt.Sprint(first["email"]) == "" || fmt.Sprint(first["email"]) == "<nil>" {
			t.Fatalf("signature row: %s", sigs.Body)
		}
	})
}
