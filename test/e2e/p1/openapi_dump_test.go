//go:build e2e

package p1

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// placeholderServer replaces the test node's random base URL so the committed document is stable.
const placeholderServer = "https://emailbridge.example.com"

// TestDumpOpenAPI writes the OpenAPI document (YAML) of a full-role node to $EB_E2E_DUMP_OPENAPI. Opt-in:
// `task openapi` writes api/openapi/openapi.yaml, and the e2e workflow uses it as a drift check (S2 §1).
// Every module registers its routes when it starts, so a booted node is the complete source of truth.
func TestDumpOpenAPI(t *testing.T) {
	out := os.Getenv("EB_E2E_DUMP_OPENAPI")
	if out == "" {
		t.Skip("set EB_E2E_DUMP_OPENAPI=<file> to dump the OpenAPI document")
	}
	n := harness.StartP1Node(t, nil)
	resp, err := http.Get(n.Base + "/v1/openapi.yaml") //nolint:noctx // e2e
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi: %d %v", resp.StatusCode, err)
	}
	raw = bytes.ReplaceAll(raw, []byte(n.Base), []byte(placeholderServer))
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes to %s", len(raw), out)
}
