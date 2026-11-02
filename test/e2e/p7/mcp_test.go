//go:build e2e

package p7

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/test/sink"
)

func TestP7_MCP_ListAccountsAndGates(t *testing.T) {
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{})
	enableOrgSwitch(t, f.Owner, "mcp.enabled", true)

	_, token := f.Owner.CreateAPIKey(t, "mcp-e2e", []string{"mcp:use", "accounts:read", "messages:read"})
	sess := mcpSession(t, f.Node.Base, token)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list_accounts",
		Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("list_accounts: %v", err)
	}
	text := mcpText(res)
	if !strings.Contains(text, f.AccID) && !strings.Contains(text, f.Email) {
		t.Fatalf("list_accounts missing account: %s", text)
	}

	// Session user → forbidden.
	userHit := f.Owner.Do(http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
		},
	})
	if userHit.Status < 400 {
		t.Fatalf("session MCP want 4xx, got %d %s", userHit.Status, userHit.Body)
	}

	// Feature off.
	enableOrgSwitch(t, f.Owner, "mcp.enabled", false)
	bearer := f.Node.WithBearer(token)
	off := bearer.Do(http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
		},
	})
	if off.Status != http.StatusForbidden || problemCode(off) != "feature_disabled" {
		t.Fatalf("mcp off: want 403 feature_disabled, got %d %s", off.Status, off.Body)
	}
}
