//go:build e2e

package p4

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Message get with text/headers/envelope, raw download, and threadId linkage.
func TestP4_MessageGetRawTextThread(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.received"}})
	f.Sink.Reset()

	parentMID := fmt.Sprintf("<p4-thread-parent-%d@e2e>", time.Now().UnixNano())
	childMID := fmt.Sprintf("<p4-thread-child-%d@e2e>", time.Now().UnixNano())
	f.inject("Thread root", parentMID, "root body")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	parent := f.waitMsgBySubject("Thread root", 60*time.Second)

	childRaw := []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Re: Thread root\r\nMessage-ID: %s\r\nIn-Reply-To: %s\r\nReferences: %s\r\nDate: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nreply body\r\n",
		f.Email, f.Email, childMID, parentMID, parentMID, time.Now().UTC().Format(time.RFC1123Z),
	))
	f.injectRaw("INBOX", childRaw, nil, time.Time{})
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	child := f.waitMsgBySubject("Re: Thread root", 60*time.Second)

	pID, _ := parent["id"].(string)
	cID, _ := child["id"].(string)
	pThread, _ := parent["threadId"].(string)
	cThread, _ := child["threadId"].(string)
	if pThread == "" || pThread != cThread {
		t.Fatalf("threadId mismatch parent=%q child=%q", pThread, cThread)
	}

	view := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+pID+"?text=both&headers=1&envelope=1", nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	if view.Str("text") == "" && view.JSON["text"] == nil {
		t.Fatalf("want text: %s", view.Body)
	}
	if view.JSON["headers"] == nil && view.JSON["from"] == nil {
		t.Fatalf("want headers or envelope: %s", view.Body)
	}

	raw := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+pID+"/raw", nil)
	harness.ExpectStatus(t, raw, http.StatusOK)
	if !strings.Contains(string(raw.Body), "Thread root") {
		t.Fatalf("raw missing subject: %s", truncate(raw.Body, 200))
	}

	byThread := f.Owner.Do(http.MethodGet, f.base()+"/messages?threadId="+pThread+"&limit=20", nil)
	harness.ExpectStatus(t, byThread, http.StatusOK)
	data, _ := byThread.JSON["data"].([]any)
	ids := map[string]bool{}
	for _, row := range data {
		m, _ := row.(map[string]any)
		if m != nil {
			ids[m["id"].(string)] = true
		}
	}
	if !ids[pID] || !ids[cID] {
		t.Fatalf("thread filter missing members: %v", ids)
	}
}

// Flags, move, delete and batch actions.
func TestP4_FlagsMoveDeleteBulk(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.received", "message.updated", "message.deleted"}})
	inbox := f.inboxID()
	archive := f.mailboxID("Archive")
	f.Sink.Reset()

	f.inject("flag-me", "<p4-flag@e2e>", "x")
	f.inject("move-me", "<p4-move@e2e>", "y")
	f.inject("del-me", "<p4-del@e2e>", "z")
	f.inject("bulk-a", "<p4-ba@e2e>", "a")
	f.inject("bulk-b", "<p4-bb@e2e>", "b")
	for _, subj := range []string{"flag-me", "move-me", "del-me", "bulk-a", "bulk-b"} {
		_ = f.waitMsgBySubject(subj, 60*time.Second)
	}

	flagMsg := f.waitMsgBySubject("flag-me", 5*time.Second)
	flagID, _ := flagMsg["id"].(string)
	patch := f.Owner.Do(http.MethodPatch, f.base()+"/messages/"+flagID, map[string]any{
		"addFlags": []string{`\Flagged`, `\Seen`},
	})
	harness.ExpectStatus(t, patch, http.StatusOK)
	_ = f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{"mode": "full"})
	f.Owner.WaitJSON(t, f.base()+"/messages/"+flagID, 45*time.Second, "flagged", func(r harness.Resp) bool {
		s := strings.Join(asStringSlice(r.JSON["flags"]), " ")
		return r.Status == http.StatusOK && (strings.Contains(s, "Flagged") || strings.Contains(s, `\Flagged`))
	})

	moveMsg := f.waitMsgBySubject("move-me", 5*time.Second)
	moveID, _ := moveMsg["id"].(string)
	mv := f.Owner.Do(http.MethodPost, f.base()+"/messages/"+moveID+"/move", map[string]any{"mailboxId": archive})
	if mv.Status != http.StatusNoContent && mv.Status != http.StatusOK && mv.Status != http.StatusAccepted {
		t.Fatalf("move = %d %s", mv.Status, mv.Body)
	}
	// Index updates only after sync sees the IMAP MOVE (no optimistic write).
	_ = f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{"mode": "full"})
	movedOK := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+moveID, nil)
		if r.Status == http.StatusOK && r.Str("mailboxId") == archive {
			movedOK = true
			break
		}
		// Message id may change on some paths; also accept subject appearing under Archive.
		list := f.Owner.Do(http.MethodGet, f.base()+"/messages?mailboxId="+archive+"&limit=50", nil)
		if list.Status == http.StatusOK {
			data, _ := list.JSON["data"].([]any)
			for _, raw := range data {
				m, _ := raw.(map[string]any)
				if m != nil && m["subject"] == "move-me" {
					movedOK = true
					break
				}
			}
		}
		if movedOK {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if !movedOK {
		t.Fatal("timeout waiting for moved message in Archive index")
	}

	delMsg := f.waitMsgBySubject("del-me", 5*time.Second)
	delID, _ := delMsg["id"].(string)
	del := f.Owner.Do(http.MethodDelete, f.base()+"/messages/"+delID, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete = %d %s", del.Status, del.Body)
	}

	ba := f.waitMsgBySubject("bulk-a", 5*time.Second)
	bb := f.waitMsgBySubject("bulk-b", 5*time.Second)
	baID, _ := ba["id"].(string)
	bbID, _ := bb["id"].(string)
	batch := f.Owner.Do(http.MethodPost, f.base()+"/messages/batch", map[string]any{
		"ids": []string{baID, bbID}, "action": "flags", "addFlags": []string{`\Seen`},
	})
	harness.ExpectStatus(t, batch, http.StatusOK)
	if n, _ := batch.JSON["processed"].(float64); int(n) < 1 {
		t.Fatalf("batch flags: %s", batch.Body)
	}
	batchDel := f.Owner.Do(http.MethodPost, f.base()+"/messages/batch", map[string]any{
		"ids": []string{baID}, "action": "delete",
	})
	if batchDel.Status != http.StatusOK {
		t.Fatalf("batch delete = %d %s", batchDel.Status, batchDel.Body)
	}
	_ = inbox
}

// Upload APPEND with a custom internalDate, then IMAP search (header / size / UID).
func TestP4_UploadAndSearch(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.received"}})
	inbox := f.inboxID()
	when := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	raw := harness.RawMessage(f.Email, f.Email, "Uploaded P4", "<p4-up@e2e>", "upload-body-unique")
	up := f.Owner.Do(http.MethodPost, f.base()+"/messages", map[string]any{
		"mailboxId": inbox, "raw": b64(raw), "flags": []string{`\Seen`},
		"internalDate": when.Format(time.RFC3339),
	})
	harness.ExpectStatus(t, up, http.StatusCreated)
	if up.JSON["uid"] == nil {
		t.Fatalf("upload missing uid: %s", up.Body)
	}

	msg := f.waitMsgBySubject("Uploaded P4", 90*time.Second)
	msgID, _ := msg["id"].(string)
	uidF, _ := msg["uid"].(float64)
	uid := uint32(uidF)

	// Header search.
	hs := f.Owner.Do(http.MethodPost, f.base()+"/messages/search", map[string]any{
		"mailboxId": inbox,
		"header":    map[string]any{"name": "Subject", "value": "Uploaded P4"},
	})
	harness.ExpectStatus(t, hs, http.StatusOK)
	if !searchHasID(hs, msgID) {
		t.Fatalf("header search missed: %s", hs.Body)
	}

	// Size: message should be larger than 20 bytes.
	larger := int64(20)
	sz := f.Owner.Do(http.MethodPost, f.base()+"/messages/search", map[string]any{
		"mailboxId": inbox, "larger": larger, "subject": "Uploaded P4",
	})
	harness.ExpectStatus(t, sz, http.StatusOK)
	if !searchHasID(sz, msgID) {
		t.Fatalf("size search missed: %s", sz.Body)
	}

	// UID range.
	uidMin, uidMax := uid, uid
	ur := f.Owner.Do(http.MethodPost, f.base()+"/messages/search", map[string]any{
		"mailboxId": inbox, "uidMin": uidMin, "uidMax": uidMax,
	})
	harness.ExpectStatus(t, ur, http.StatusOK)
	if !searchHasID(ur, msgID) {
		t.Fatalf("uid search missed: %s", ur.Body)
	}

	// Permission: messages:read key cannot mutate.
	_, token := f.Owner.CreateAPIKey(t, "p4-read", []string{"messages:read", "accounts:read"})
	reader := f.Node.WithBearer(token)
	ref := reader.Do(http.MethodPatch, f.base()+"/messages/"+msgID, map[string]any{"addFlags": []string{`\Flagged`}})
	if ref.Status != http.StatusForbidden && ref.Status != http.StatusUnauthorized {
		t.Fatalf("read-only key mutate = %d %s", ref.Status, ref.Body)
	}
}

func searchHasID(r harness.Resp, id string) bool {
	data, _ := r.JSON["data"].([]any)
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		if m != nil && m["id"] == id {
			return true
		}
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
