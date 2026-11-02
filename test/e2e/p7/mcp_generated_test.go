//go:build e2e

package p7

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// The dedicated MCP port (mcp.listen, set at runtime) serves generated API tools: a read tool and a write
// tool run through the API with the caller's own key, so the key's scopes decide (docs/spec/MCP-server.md).
func TestP10_MCP_GeneratedToolsOnDedicatedPort(t *testing.T) {
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{})
	enableOrgSwitch(t, f.Owner, "mcp.enabled", true)

	r := f.Owner.Do(http.MethodPut, "/v1/mcp/server", map[string]any{"listen": "127.0.0.1:0"})
	harness.ExpectStatus(t, r, http.StatusOK)
	addr := r.Str("activeAddr")
	if addr == "" {
		t.Fatalf("dedicated listener did not start: %s", r.Body)
	}
	t.Cleanup(func() { f.Owner.Do(http.MethodPut, "/v1/mcp/server", map[string]any{"listen": ""}) })
	base := "http://" + addr

	_, readKey := f.Owner.CreateAPIKey(t, "mcp-gen-read", []string{"mcp:use", "accounts:read"})
	_, writeKey := f.Owner.CreateAPIKey(t, "mcp-gen-write", []string{"mcp:use", "accounts:write"})

	read := mcpSession(t, base, readKey)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := read.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range list.Tools {
		have[tl.Name] = true
	}
	raw, _ := json.Marshal(list)
	t.Logf("tools/list: %d tools, %d bytes", len(list.Tools), len(raw))
	for _, want := range []string{"list_accounts", "get_account", "patch_account", "get_ai_settings"} {
		if !have[want] {
			t.Fatalf("tools/list misses %s (%d tools)", want, len(list.Tools))
		}
	}

	res, err := read.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_account", Arguments: map[string]any{"accountId": f.AccID}})
	if err != nil || res.IsError || !strings.Contains(mcpText(res), f.AccID) {
		t.Fatalf("get_account: %v %s", err, mcpText(res))
	}
	patch := map[string]any{"accountId": f.AccID, "body": map[string]any{"name": "Renamed by MCP"}}
	res, err = read.CallTool(ctx, &sdkmcp.CallToolParams{Name: "patch_account", Arguments: patch})
	if err != nil || !res.IsError || !strings.Contains(mcpText(res), "scope_missing") {
		t.Fatalf("patch_account with a read key: %v %s", err, mcpText(res))
	}
	write := mcpSession(t, base, writeKey)
	res, err = write.CallTool(ctx, &sdkmcp.CallToolParams{Name: "patch_account", Arguments: patch})
	if err != nil || res.IsError || !strings.Contains(mcpText(res), "Renamed by MCP") {
		t.Fatalf("patch_account: %v %s", err, mcpText(res))
	}

	// The main listener keeps serving MCP at /v1/mcp; the dedicated port serves nothing else.
	main := mcpSession(t, f.Node.Base+"/v1", readKey)
	if _, err := main.ListTools(ctx, &sdkmcp.ListToolsParams{}); err != nil {
		t.Fatalf("/v1/mcp: %v", err)
	}
	other, err := http.Get(base + "/v1/accounts")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Body.Close()
	if other.StatusCode != http.StatusNotFound {
		t.Fatalf("REST on the MCP port: %d", other.StatusCode)
	}
}
