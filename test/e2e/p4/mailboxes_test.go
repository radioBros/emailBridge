//go:build e2e

package p4

import (
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Mailboxes CRUD through the public API (P4.1). Index rows appear after sync lists the folder.
func TestP4_MailboxesCRUD(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"*"}})
	_ = f.inboxID()

	path := "P4Folder"
	created := f.Owner.Do(http.MethodPost, f.base()+"/mailboxes", map[string]any{"path": path, "subscribe": true})
	harness.ExpectStatus(t, created, http.StatusCreated)
	nudgeSync(f)
	mbxID := f.mailboxID(path)

	got := f.Owner.Do(http.MethodGet, f.base()+"/mailboxes/"+mbxID, nil)
	if got.Status != http.StatusOK {
		logs := f.Owner.Do(http.MethodGet, "/v1/logs?limit=20&level=error&from="+time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)+"&to="+time.Now().Add(time.Minute).UTC().Format(time.RFC3339), nil)
		t.Fatalf("GET …/mailboxes/%s = %d %s; error logs: %.4000s", mbxID, got.Status, got.Body, logs.Body)
	}
	if got.Str("path") != path {
		if p, _ := got.JSON["path"].(string); p != path {
			t.Fatalf("get mailbox: %s", got.Body)
		}
	}

	renamed := "P4Renamed"
	patch := f.Owner.Do(http.MethodPatch, f.base()+"/mailboxes/"+mbxID, map[string]any{"path": renamed})
	if patch.Status != http.StatusOK {
		t.Fatalf("PATCH …/mailboxes/%s rename = %d %s", mbxID, patch.Status, patch.Body)
	}
	nudgeSync(f)
	// A renamed folder is a new mailbox for sync (P4.1): delete it by its new id.
	renamedID := f.mailboxID(renamed)

	del := f.Owner.Do(http.MethodDelete, f.base()+"/mailboxes/"+renamedID, nil)
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete = %d %s", del.Status, del.Body)
	}
	nudgeSync(f)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		list := f.Owner.Do(http.MethodGet, f.base()+"/mailboxes?limit=100", nil)
		if list.Status == http.StatusOK {
			found := false
			data, _ := list.JSON["data"].([]any)
			for _, raw := range data {
				m, _ := raw.(map[string]any)
				if m != nil && (m["path"] == renamed || m["path"] == path || m["id"] == mbxID || m["id"] == renamedID) {
					found = true
					break
				}
			}
			if !found {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("deleted mailbox still listed")
}

func nudgeSync(f *fixture) {
	f.t.Helper()
	r := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/sync", map[string]any{"mode": "full"})
	if r.Status != http.StatusAccepted && r.Status != http.StatusOK && r.Status != http.StatusNoContent {
		f.t.Logf("sync nudge = %d %s", r.Status, r.Body)
	}
}

// Validation refusal: empty mailbox path.
func TestP4_MailboxValidation(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.received"}})
	r := f.Owner.Do(http.MethodPost, f.base()+"/mailboxes", map[string]any{"path": ""})
	if r.Status < 400 {
		t.Fatalf("empty path should fail, got %d %s", r.Status, r.Body)
	}
}
