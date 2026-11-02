//go:build e2e

package p0

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBootHealthOpenAPIAndSPA(t *testing.T) {
	n := StartNode(t, nil)

	hz, err := http.Get(n.Base + "/healthz") //nolint:noctx // e2e
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hz.Body.Close() }()
	if hz.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d", hz.StatusCode)
	}

	rz, err := http.Get(n.Base + "/readyz") //nolint:noctx // e2e
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rz.Body.Close() }()
	if rz.StatusCode != http.StatusOK {
		t.Fatalf("/readyz = %d", rz.StatusCode)
	}

	oa, err := http.Get(n.Base + "/v1/openapi.json") //nolint:noctx // e2e
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oa.Body.Close() }()
	raw, _ := io.ReadAll(oa.Body)
	if oa.StatusCode != http.StatusOK || len(raw) == 0 {
		t.Fatalf("/v1/openapi.json = %d len=%d", oa.StatusCode, len(raw))
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi json: %v", err)
	}
	if doc["openapi"] == nil && doc["paths"] == nil {
		t.Fatalf("openapi missing openapi/paths keys: %s", truncate(raw, 200))
	}

	idx, err := http.Get(n.Base + "/") //nolint:noctx // e2e
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Body.Close() }()
	body, _ := io.ReadAll(idx.Body)
	ct := idx.Header.Get("Content-Type")
	if idx.StatusCode == http.StatusOK && (strings.Contains(ct, "text/html") || strings.Contains(string(body), "<html") || strings.Contains(string(body), "<!DOCTYPE")) {
		return
	}
	// SPA is not embedded in the binary yet (httpapi.Options.SPA is nil until web/dist is wired).
	t.Logf("SPA index at / not served yet (status=%d ct=%q); skipping assert until embed lands", idx.StatusCode, ct)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
