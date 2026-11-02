//go:build e2e

package p7

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func TestP7_Exports_CreateDownloadAndGate(t *testing.T) {
	f := startFixture(t, map[string]any{"inbound.archiveRaw": true}, []string{"message.received"}, sink.Options{})

	// Feature off by default.
	denied := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/exports", map[string]any{
		"include": map[string]any{"metadataOnly": true},
	})
	if denied.Status != http.StatusForbidden || problemCode(denied) != "feature_disabled" {
		t.Fatalf("export off: want 403 feature_disabled, got %d %s", denied.Status, denied.Body)
	}

	enableOrgSwitch(t, f.Owner, "export.enabled", true)

	subj := fmt.Sprintf("export-%d", time.Now().UnixNano())
	f.inject(subj, "<"+subj+"@e2e>", "export me")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	_ = waitMessageID(t, f.Owner, f.AccID, subj, 30*time.Second)

	created := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/exports", map[string]any{
		"include": map[string]any{"metadataOnly": true},
	})
	if created.Status != http.StatusAccepted {
		t.Fatalf("create export: %d %s", created.Status, created.Body)
	}
	expObj, _ := created.JSON["export"].(map[string]any)
	expID, _ := expObj["id"].(string)
	if expID == "" {
		expID = created.Str("id")
	}
	if expID == "" {
		t.Fatalf("export id missing: %s", created.Body)
	}

	done := f.Owner.WaitJSON(t, "/v1/accounts/"+f.AccID+"/exports/"+expID, 90*time.Second, "export terminal", func(r harness.Resp) bool {
		st := r.Str("state")
		return r.Status == http.StatusOK && (st == "succeeded" || st == "failed" || st == "cancelled")
	})
	if done.Str("state") != "succeeded" {
		t.Fatalf("export state=%s body=%s", done.Str("state"), done.Body)
	}

	dl := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/exports/"+expID+"/download", nil)
	if dl.Status != http.StatusOK {
		t.Fatalf("download: %d %s", dl.Status, dl.Body)
	}
	// net/http may already gunzip Content-Encoding: gzip for us.
	plain := dl.Body
	if dl.Header.Get("Content-Encoding") == "gzip" || (len(plain) >= 2 && plain[0] == 0x1f && plain[1] == 0x8b) {
		zr, err := gzip.NewReader(bytes.NewReader(dl.Body))
		if err != nil {
			t.Fatalf("gzip: %v (len=%d ct=%s ce=%s)", err, len(dl.Body), dl.Header.Get("Content-Type"), dl.Header.Get("Content-Encoding"))
		}
		plain, err = io.ReadAll(zr)
		_ = zr.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(plain, []byte(subj)) && !bytes.Contains(plain, []byte(`"id"`)) {
		t.Fatalf("ndjson missing expected content: %s", truncate(plain, 400))
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
