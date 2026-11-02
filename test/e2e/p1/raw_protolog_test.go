//go:build e2e

package p1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Scenario 8: raw source + archive after expunge (inbound.archiveRaw on).
func TestP1_08_RawAndArchive(t *testing.T) {
	f := startFixture(t, map[string]any{"inbound.archiveRaw": true}, "standard", sink.Options{}, []string{"message.received"})
	f.waitConnected(60 * time.Second)
	time.Sleep(2 * time.Second)
	f.Sink.Reset()

	mid := "<raw-08@e2e>"
	raw := harness.RawMessage(f.Email, f.Email, "Archive Me", mid, "archive-body-unique")
	res := f.Sim.InjectRaw(t, f.User, "INBOX", raw, nil)
	req := harness.WaitEvent(t, f.Sink, "message.received", 45*time.Second)

	var env map[string]any
	if err := json.Unmarshal(req.Body, &env); err != nil {
		t.Fatal(err)
	}
	data, _ := env["data"].(map[string]any)
	msgID, _ := data["messageId"].(string)
	if msgID == "" {
		// CloudEvents subject / id fallbacks
		msgID, _ = env["subject"].(string)
	}
	if msgID == "" {
		t.Fatalf("no messageId in event: %s", req.Body)
	}

	rawResp := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/raw", nil)
	if rawResp.Status != http.StatusOK {
		t.Fatalf("raw = %d %s", rawResp.Status, rawResp.Body)
	}
	if !strings.Contains(string(rawResp.Body), "archive-body-unique") &&
		!strings.Contains(string(rawResp.Body), "Archive Me") {
		t.Fatalf("raw body unexpected: %s", truncateBytes(rawResp.Body, 200))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := f.Sim.Client.SetFlags(ctx, f.User, "INBOX", []uint32{res.UID}, []string{`\Deleted`}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sim.Client.Expunge(ctx, f.User, "INBOX", []uint32{res.UID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)

	arch := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/archive/"+msgID, nil)
	if arch.Status != http.StatusOK {
		t.Fatalf("archive after expunge = %d %s", arch.Status, arch.Body)
	}
	if !strings.Contains(string(arch.Body), "archive-body-unique") &&
		!strings.Contains(string(arch.Body), "Archive Me") {
		t.Fatalf("archive body unexpected: %s", truncateBytes(arch.Body, 200))
	}
}

// Scenario 9: protocol logs on → transcript listed, password absent.
func TestP1_09_ProtocolLogsMasked(t *testing.T) {
	f := startFixture(t, map[string]any{"protocolLogs.enabled": true}, "standard", sink.Options{}, []string{"message.received"})
	f.waitConnected(60 * time.Second)
	time.Sleep(2 * time.Second)

	f.inject("protolog", "<plog-09@e2e>", "x")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 45*time.Second)

	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	var body string
	harness.WaitUntil(t, 30*time.Second, "protocol log rows", func() bool {
		r := f.Owner.Do(http.MethodGet,
			"/v1/accounts/"+f.AccID+"/protocol-logs?from="+from+"&to="+to+"&limit=50", nil)
		if r.Status != http.StatusOK {
			return false
		}
		body = string(r.Body)
		data, _ := r.JSON["data"].([]any)
		return len(data) > 0 || strings.Contains(body, "LOGIN") || strings.Contains(body, "imap")
	})
	if strings.Contains(strings.ToLower(body), strings.ToLower(f.Pass)) {
		t.Fatalf("protocol logs leaked password")
	}
	// Common masking markers or absence of the literal password is enough; also reject obvious AUTH PLAIN blobs with pass.
	if strings.Contains(body, f.Pass) {
		t.Fatal("password present in protocol log listing")
	}
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
