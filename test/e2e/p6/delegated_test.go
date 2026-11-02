//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	simgraph "github.com/radiobros/emailbridge/simulator/graph"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Delegated / shared Microsoft 365 mailboxes (W54 G24).
func TestP6_Delegated_Mailbox(t *testing.T) {
	sim := simgraph.New()
	t.Cleanup(sim.Close)
	f := startBaseWithAPI(t, "", sim.URL())
	stamp := time.Now().UnixNano()

	parentMail := fmt.Sprintf("parent%d@e2e.test", stamp)
	sharedMail := fmt.Sprintf("shared%d@e2e.test", stamp)
	sim.EnsureUser(parentMail)
	sim.EnsureUser(sharedMail)

	ao := true
	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("del-ao-%d", stamp), "provider": "outlook",
		"clientId": "cid", "clientSecret": "csec",
		"appOnly": ao, "tenantId": "common", "tokenUrl": sim.URL() + "/token",
	})

	parent := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Delegate parent", parentMail, "graph", map[string]any{
			"oauth": map[string]any{"appId": app.Str("id")},
		},
	))
	harness.ExpectStatus(t, parent, http.StatusCreated)
	parentID := parent.Str("id")

	// Same-org delegated child OK.
	child := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Shared mbx", sharedMail, "graph", map[string]any{
			"delegated": map[string]any{"account": parentID, "user": sharedMail},
		},
	))
	harness.ExpectStatus(t, child, http.StatusCreated)
	childID := child.Str("id")
	got := f.Owner.Do(http.MethodGet, "/v1/accounts/"+childID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	del, _ := got.JSON["delegated"].(map[string]any)
	if del == nil || del["account"] != parentID {
		t.Fatalf("delegated field: %s", got.Body)
	}

	// Other-org parent id → 404 (not 422 leaking existence).
	b, _ := orgBSession(t, f.Node, f.Owner)
	bApp := createOAuthApp(t, b, map[string]any{
		"name": fmt.Sprintf("b-ao-%d", stamp), "provider": "outlook",
		"clientId": "cid", "clientSecret": "csec",
		"appOnly": ao, "tenantId": "common", "tokenUrl": sim.URL() + "/token",
	})
	foreign := b.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Foreign del", fmt.Sprintf("foreign%d@e2e.test", stamp), "graph", map[string]any{
			"oauth":     map[string]any{"appId": bApp.Str("id")},
			"delegated": map[string]any{"account": parentID, "user": sharedMail},
		},
	))
	if foreign.Status != http.StatusNotFound {
		harness.ExpectProblem(t, foreign, http.StatusNotFound, "not_found")
	}

	// Delete parent while referenced → 409 delegated_in_use.
	delParent := f.Owner.Do(http.MethodDelete, "/v1/accounts/"+parentID, nil)
	if delParent.Status != http.StatusConflict || problemCode(delParent) != "delegated_in_use" {
		cur := f.Owner.Do(http.MethodGet, "/v1/accounts/"+parentID, nil)
		delParent = withIfMatch(f.Owner, etagOf(cur), func() harness.Resp {
			return f.Owner.Do(http.MethodDelete, "/v1/accounts/"+parentID, nil)
		})
	}
	if delParent.Status != http.StatusConflict {
		t.Fatalf("want 409 delegated_in_use, got %d %s", delParent.Status, delParent.Body)
	}
	if code := problemCode(delParent); code != "delegated_in_use" && code != "conflict" {
		t.Fatalf("code=%s body=%s", code, delParent.Body)
	}

	// Shared mailbox ops: inject only into shared user; child sync/get must see it (UPN=shared).
	sub := fmt.Sprintf("delegated-in-%d", stamp)
	sim.PutMessage(sharedMail, rfc822("peer@e2e.test", sharedMail, sub, "shared body"), "inbox")
	waitAccountConnected(t, f.Owner, childID, 60*time.Second)
	nudgeSync(t, f.Owner, childID)
	msg := waitMsgSubject(t, f.Owner, childID, sub, 60*time.Second)
	msgID, _ := msg["id"].(string)
	if msgID == "" {
		t.Fatalf("delegated sync missed shared message: %v", msg)
	}
	one := f.Owner.Do(http.MethodGet, "/v1/accounts/"+childID+"/messages/"+msgID, nil)
	harness.ExpectStatus(t, one, http.StatusOK)
	if one.Str("subject") != sub {
		t.Fatalf("delegated get: %s", one.Body)
	}
	flags := f.Owner.Do(http.MethodPatch, "/v1/accounts/"+childID+"/messages/"+msgID, map[string]any{
		"addFlags": []string{`\Seen`},
	})
	harness.ExpectStatus(t, flags, http.StatusOK)
}
