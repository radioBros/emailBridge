//go:build integration

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// TestIntegrationGeneratedToolsOnDedicatedPort runs MCP over the dedicated listener with API keys: the
// generated tools of a real module (AI settings, MongoDB-backed) appear in tools/list, a read tool and a
// write tool work, and the key's scopes decide like on the REST API.
func TestIntegrationGeneratedToolsOnDedicatedPort(t *testing.T) {
	db := testDB(t)
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	srv.AddMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes := map[string][]string{
				"Bearer ebk_read":  {"mcp:use", "settings:read"},
				"Bearer ebk_write": {"mcp:use", "settings:write"},
			}[r.Header.Get("Authorization")]
			if scopes == nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := store.WithOrganization(r.Context(), org)
			ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_it", OrganizationID: org, Scopes: scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	aiSvc := &ai.Service{Repo: &ai.Repo{DB: db}, DB: db, Registry: features.DefaultRegistry(), Env: "test"}
	aiSvc.Register(srv)
	h := &Handler{Gateway: &Gateway{}, ForceEnabled: true}
	h.Mount(srv)

	l := &Listener{Handler: srv}
	st := l.Apply(context.Background(), "127.0.0.1:0")
	if st.Active == "" {
		t.Fatalf("listener %+v", st)
	}
	t.Cleanup(func() { l.Stop(context.Background()) })
	base := "http://" + st.Active

	read := connect(t, base+"/mcp?toolsets=search,curated", "ebk_read")
	names := toolNames(t, read)
	for _, want := range []string{"get_ai_settings", "put_ai_settings", "list_ai_models", "ai_summarize_message", "list_accounts"} {
		if !names[want] {
			t.Fatalf("tools/list misses %s: %v", want, names)
		}
	}
	text, isErr := callText(t, read, "get_ai_settings", nil)
	if isErr || !strings.Contains(text, `"provider":"openai"`) {
		t.Fatalf("get_ai_settings: %v %s", isErr, text)
	}
	put := map[string]any{"body": map[string]any{"provider": "anthropic", "model": "claude-haiku-4-5"}}
	text, isErr = callText(t, read, "put_ai_settings", put)
	if !isErr || !strings.Contains(text, "scope_missing") {
		t.Fatalf("put with a read key must fail: %v %s", isErr, text)
	}

	write := connect(t, base+"/mcp", "ebk_write")
	text, isErr = callText(t, write, "put_ai_settings", put)
	if isErr || !strings.Contains(text, `"provider":"anthropic"`) {
		t.Fatalf("put_ai_settings: %v %s", isErr, text)
	}
	text, isErr = callText(t, read, "get_ai_settings", nil)
	if isErr || !strings.Contains(text, `"model":"claude-haiku-4-5"`) {
		t.Fatalf("stored settings: %v %s", isErr, text)
	}
	// The dedicated port serves nothing but MCP.
	res, err := http.Get(base + "/v1/ai/settings")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("REST on the MCP port: %d", res.StatusCode)
	}
}
