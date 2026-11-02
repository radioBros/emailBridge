//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func rfc822(from, to, subject, body string) []byte {
	mid := fmt.Sprintf("<%d@e2e.test>", time.Now().UnixNano())
	return []byte("From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMessage-ID: " + mid + "\r\nDate: Mon, 26 Oct 2026 12:00:00 +0000\r\n\r\n" + body + "\r\n")
}

func TestP6_Gmail_ServiceAccount_Link(t *testing.T) {
	gsim := simgmail.New()
	t.Cleanup(gsim.Close)
	apiRoot := wrapGmailAPI(t, gsim.URL())
	f := startBaseWithAPI(t, apiRoot, "")
	stamp := time.Now().UnixNano()
	mailbox := fmt.Sprintf("guser%d@e2e.test", stamp)
	gsim.EnsureUser(mailbox)

	sk := serviceAccountJSON(t, gsim.URL()+"/token")
	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("gmail-sa-%d", stamp), "provider": "gmail",
		"clientId": "cid", "clientSecret": "csec",
		"serviceKey": sk, "tokenUrl": gsim.URL() + "/token",
	})
	if app.JSON["hasServiceKey"] != true {
		t.Fatalf("hasServiceKey: %s", app.Body)
	}

	acc := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Gmail SA", mailbox, "gmail", map[string]any{
			"oauth": map[string]any{"appId": app.Str("id")},
		},
	))
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")

	view := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth", nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	if view.JSON["connected"] != true {
		t.Fatalf("gmail oauth view: %s", view.Body)
	}

	sub := fmt.Sprintf("gmail-sa-in-%d", stamp)
	delSub := fmt.Sprintf("gmail-sa-del-%d", stamp)
	gsim.PutMessage(mailbox, rfc822("peer@e2e.test", mailbox, sub, "hi"), []string{"INBOX", "UNREAD"})
	gsim.PutMessage(mailbox, rfc822("peer@e2e.test", mailbox, delSub, "bye"), []string{"INBOX", "UNREAD"})

	waitAccountConnected(t, f.Owner, accID, 60*time.Second)
	nudgeSync(t, f.Owner, accID)
	msg := waitMsgSubject(t, f.Owner, accID, sub, 60*time.Second)
	msgID, _ := msg["id"].(string)
	if msgID == "" {
		t.Fatalf("missing message id: %v", msg)
	}
	delMsg := waitMsgSubject(t, f.Owner, accID, delSub, 60*time.Second)
	delID, _ := delMsg["id"].(string)

	got := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/messages/"+msgID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	if got.Str("subject") != sub {
		t.Fatalf("get message: %s", got.Body)
	}

	inboxID := mailboxByPath(t, f.Owner, accID, "INBOX")
	search := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/messages/search", map[string]any{
		"mailboxId": inboxID, "gmailRaw": "subject:(" + sub + ")",
	})
	harness.ExpectStatus(t, search, http.StatusOK)
	foundSearch := false
	for _, raw := range search.JSON["data"].([]any) {
		m, _ := raw.(map[string]any)
		if m != nil && (m["id"] == msgID || m["subject"] == sub) {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Fatalf("gmailRaw search missed: %s", search.Body)
	}

	flags := f.Owner.Do(http.MethodPatch, "/v1/accounts/"+accID+"/messages/"+msgID, map[string]any{
		"addFlags": []string{`\Seen`, `\Flagged`},
	})
	harness.ExpectStatus(t, flags, http.StatusOK)

	trashID := mailboxByPath(t, f.Owner, accID, "[Gmail]/Trash")
	moved := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/messages/"+msgID+"/move", map[string]any{
		"mailboxId": trashID,
	})
	if moved.Status != http.StatusNoContent && moved.Status != http.StatusOK && moved.Status != http.StatusAccepted {
		t.Fatalf("move: %d %s", moved.Status, moved.Body)
	}

	del := f.Owner.Do(http.MethodDelete, "/v1/accounts/"+accID+"/messages/"+delID, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Status, del.Body)
	}

	outSub := fmt.Sprintf("gmail-sa-out-%d", stamp)
	send := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
		"to": []map[string]any{{"address": mailbox}}, "subject": outSub, "text": "sa send",
	})
	harness.ExpectStatus(t, send, http.StatusAccepted)
	if !waitOutboundSent(t, f.Owner, send.Str("id"), 45*time.Second) {
		t.Fatal("send through the provider API did not reach sent")
	}
}

func TestP6_Graph_AppOnly_Link(t *testing.T) {
	gsim := simgraph.New()
	t.Cleanup(gsim.Close)
	f := startBaseWithAPI(t, "", gsim.URL())
	stamp := time.Now().UnixNano()
	mailbox := fmt.Sprintf("graph%d@e2e.test", stamp)
	gsim.EnsureUser(mailbox)

	ao := true
	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("graph-ao-%d", stamp), "provider": "outlook",
		"clientId": "cid", "clientSecret": "csec",
		"appOnly": ao, "tenantId": "common", "tokenUrl": gsim.URL() + "/token",
	})

	acc := f.Owner.Do(http.MethodPost, "/v1/accounts", apiProviderBody(
		"Graph AO", mailbox, "graph", map[string]any{
			"oauth": map[string]any{"appId": app.Str("id")},
		},
	))
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")

	view := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/oauth", nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	if view.JSON["connected"] != true {
		t.Fatalf("graph oauth view: %s", view.Body)
	}

	sub := fmt.Sprintf("graph-ao-in-%d", stamp)
	delSub := fmt.Sprintf("graph-ao-del-%d", stamp)
	gsim.PutMessage(mailbox, rfc822("peer@e2e.test", mailbox, sub, "hello graph"), "inbox")
	gsim.PutMessage(mailbox, rfc822("peer@e2e.test", mailbox, delSub, "del"), "inbox")

	waitAccountConnected(t, f.Owner, accID, 60*time.Second)
	nudgeSync(t, f.Owner, accID)
	msg := waitMsgSubject(t, f.Owner, accID, sub, 60*time.Second)
	msgID, _ := msg["id"].(string)
	delMsg := waitMsgSubject(t, f.Owner, accID, delSub, 60*time.Second)
	delID, _ := delMsg["id"].(string)

	got := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/messages/"+msgID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)

	inboxID := mailboxByPath(t, f.Owner, accID, "INBOX")
	search := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/messages/search", map[string]any{
		"mailboxId": inboxID, "subject": sub, "useOutlookSearch": true,
	})
	harness.ExpectStatus(t, search, http.StatusOK)
	foundSearch := false
	data, _ := search.JSON["data"].([]any)
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		if m != nil && (m["id"] == msgID || m["subject"] == sub) {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Fatalf("graph search missed: %s", search.Body)
	}

	flags := f.Owner.Do(http.MethodPatch, "/v1/accounts/"+accID+"/messages/"+msgID, map[string]any{
		"addFlags": []string{`\Seen`, `\Flagged`},
	})
	harness.ExpectStatus(t, flags, http.StatusOK)

	trashID := mailboxByPath(t, f.Owner, accID, "Deleted Items")
	moved := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/messages/"+msgID+"/move", map[string]any{
		"mailboxId": trashID,
	})
	if moved.Status != http.StatusNoContent && moved.Status != http.StatusOK && moved.Status != http.StatusAccepted {
		t.Fatalf("move: %d %s", moved.Status, moved.Body)
	}

	del := f.Owner.Do(http.MethodDelete, "/v1/accounts/"+accID+"/messages/"+delID, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Status, del.Body)
	}

	outSub := fmt.Sprintf("graph-ao-out-%d", stamp)
	send := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
		"to": []map[string]any{{"address": mailbox}}, "subject": outSub, "text": "ao send",
	})
	harness.ExpectStatus(t, send, http.StatusAccepted)
	if !waitOutboundSent(t, f.Owner, send.Str("id"), 45*time.Second) {
		t.Fatal("send through the provider API did not reach sent")
	}
}

// XOAUTH2 path: oauth tokens from a mock IdP drive the IMAP/SMTP simulator (P6.1).
func TestP6_OAuth_XOAUTH2_IMAPSync(t *testing.T) {
	f := startBase(t)
	idp := startMockIdP(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("xoauth%d", stamp)
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	f.Sim.CreateSimUser(t, user, pass)

	app := createOAuthApp(t, f.Owner, map[string]any{
		"name": fmt.Sprintf("xoauth-%d", stamp), "provider": "generic",
		"clientId": "cid", "clientSecret": "csec",
		"authorizeUrl": idp.URL + "/authorize", "tokenUrl": idp.URL + "/token",
		"scopes": []string{"mail"},
	})

	body := harness.AccountCreateBody(t, f.Sim, user, pass, email, "XOAUTH2", map[string]any{
		"sandbox.enabled": false,
	}, "standard")
	acc := f.Owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	f.Owner.WaitJSON(t, "/v1/accounts/"+accID, 90*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	completeOAuth(t, f.Node, f.Owner, accID, app.Str("id"), "generic")
	tok := idp.lastAccessToken()
	if tok == "" {
		t.Fatal("mock IdP issued no access token")
	}
	f.Sim.Sim.SetOAuthToken(user, tok)

	harness.ExpectStatus(t, f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/reconnect", nil), http.StatusAccepted)
	f.Owner.WaitJSON(t, "/v1/accounts/"+accID, 90*time.Second, "reconnected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	subj := fmt.Sprintf("xoauth-hello-%d", stamp)
	f.Sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, subj, "", "via oauth"), nil)
	f.Owner.WaitJSON(t, "/v1/accounts/"+accID+"/messages?limit=20", 90*time.Second, "msg indexed", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["subject"] == subj {
				return true
			}
		}
		return false
	})
}
