package mcp

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ListenSettings is the subset of settings.Service the listener control uses.
type ListenSettings interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
	Patch(ctx context.Context, scope string, patch map[string]any, ifMatch string) (settings.SettingsBody, error)
	Invalidate(scope string)
}

// ToolsetView counts the tools of one toolset.
type ToolsetView struct {
	ID    string `json:"id" doc:"Toolset ID for ?toolsets= on the MCP URL" example:"messages"`
	Tools int    `json:"tools" doc:"Number of tools in the set" example:"14"`
}

// ServerView is GET/PUT /v1/mcp/server.
type ServerView struct {
	Listen        string        `json:"listen" doc:"Configured address of the dedicated MCP listener (setting mcp.listen); empty = off" example:":8090"`
	Listening     bool          `json:"listening" doc:"This node is listening on the dedicated port" example:"true"`
	ActiveAddr    string        `json:"activeAddr,omitempty" doc:"Address bound on this node" example:"[::]:8090"`
	Error         string        `json:"error,omitempty" doc:"Why this node could not bind the configured address" example:"address already in use"`
	MainPath      string        `json:"mainPath" doc:"MCP path on the main API listener (always available; /mcp works too)" example:"/v1/mcp"`
	DedicatedPath string        `json:"dedicatedPath" doc:"MCP path on the dedicated listener" example:"/mcp"`
	MainURL       string        `json:"mainUrl,omitempty" doc:"MCP URL on the main listener, from EB_PUBLIC_URL" example:"https://mail-api.example.com/v1/mcp"`
	CanChange     bool          `json:"canChange" doc:"The caller may change the listener (instance owner)" example:"true"`
	Toolsets      []ToolsetView `json:"toolsets" doc:"Tool groups and their sizes; ?toolsets=a,b on the URL selects some (default all)"`
}

// ServerPut is PUT /v1/mcp/server.
type ServerPut struct {
	Listen string `json:"listen" doc:"Port (8090) or host:port (127.0.0.1:8090) of the dedicated listener; empty string turns it off" example:"8090"`
}

// Control serves the MCP server settings API of one node.
type Control struct {
	Settings  ListenSettings
	Listener  *Listener
	Handler   *Handler
	PublicURL string
	// active is set once every module has registered its routes (Activate): before that the dedicated
	// port stays closed, so no MCP request can see (and freeze) a half-built API document.
	active atomic.Bool
}

// Activate opens the dedicated listener if mcp.listen asks for it; called after the last module that
// registers routes (cmd/emailbridge wiring), right before the main listener starts.
func (c *Control) Activate(ctx context.Context) ListenerStatus {
	c.active.Store(true)
	return c.Reconcile(ctx)
}

// Reconcile reads mcp.listen and makes the listener match (start, move or stop).
func (c *Control) Reconcile(ctx context.Context) ListenerStatus {
	if c.Listener == nil {
		return ListenerStatus{}
	}
	if !c.active.Load() {
		return c.Listener.Status()
	}
	addr := ""
	if c.Settings != nil {
		if body, err := c.Settings.Effective(ctx, ""); err == nil {
			if e, ok := body.Values[SettingListen]; ok {
				if s, ok := e.Value.(string); ok {
					addr = s
				}
			}
		} else {
			return c.Listener.Status()
		}
	}
	norm, err := NormalizeListen(addr)
	if err != nil {
		c.Listener.mu.Lock()
		c.Listener.stopLocked(ctx)
		c.Listener.status = ListenerStatus{Configured: addr, Error: "the stored address is not valid"}
		c.Listener.mu.Unlock()
		return c.Listener.Status()
	}
	return c.Listener.Apply(ctx, norm)
}

func (c *Control) view(ctx context.Context, st ListenerStatus) *ServerView {
	v := &ServerView{
		Listen: st.Configured, Listening: st.Active != "", ActiveAddr: st.Active, Error: st.Error,
		MainPath: "/v1/mcp", DedicatedPath: "/mcp", Toolsets: []ToolsetView{},
	}
	if c.PublicURL != "" {
		v.MainURL = strings.TrimRight(c.PublicURL, "/") + "/v1/mcp"
	}
	if caller, ok := tenancy.CallerFrom(ctx); ok {
		v.CanChange = caller.Allows(tenancy.PermInstanceAdmin)
	}
	if c.Handler != nil {
		counts := c.Handler.ToolsetCounts()
		for _, id := range AllToolsets {
			if n := counts[id]; n > 0 {
				v.Toolsets = append(v.Toolsets, ToolsetView{ID: id, Tools: n})
			}
		}
	}
	return v
}

type serverOut struct {
	Body ServerView
}

type serverIn struct {
	Body ServerPut
}

func (c *Control) get(ctx context.Context, _ *struct{}) (*serverOut, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	st := ListenerStatus{}
	if c.Listener != nil {
		st = c.Listener.Status()
	}
	return &serverOut{Body: *c.view(ctx, st)}, nil
}

func (c *Control) put(ctx context.Context, in *serverIn) (*serverOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	norm, err := NormalizeListen(in.Body.Listen)
	if err != nil {
		return nil, err
	}
	if c.Settings == nil {
		return nil, errs.New(errs.CodeServiceUnavailable, "Settings are not available on this node.")
	}
	var val any
	if norm != "" {
		val = norm
	}
	// Patch checks the instance-owner permission, stores the value and tells the other nodes.
	if _, err := c.Settings.Patch(ctx, "global", map[string]any{"values": map[string]any{SettingListen: val}}, ""); err != nil {
		return nil, err
	}
	st := ListenerStatus{Configured: norm}
	if c.Listener != nil && c.active.Load() {
		st = c.Listener.Apply(ctx, norm)
	}
	return &serverOut{Body: *c.view(ctx, st)}, nil
}

// Register adds GET/PUT /v1/mcp/server.
func (c *Control) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P10")
	httpapi.Register(srv, huma.Operation{
		OperationID: "getMCPServer", Method: http.MethodGet, Path: "/v1/mcp/server",
		Summary: "Get the MCP server status",
		Description: "Returns the MCP server's connection details: the dedicated listener address (runtime setting " +
			"`mcp.listen`, empty = off), whether this node listens on it (or why it could not bind), the MCP path on " +
			"the main listener (`/v1/mcp`, which always works; `/mcp` too) and the toolsets with their tool counts. " +
			"MCP itself needs an API key with `mcp:use` and the `mcp.enabled` switch. Requires the `read` " +
			"permission (API keys `settings:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"listen": ":8090", "listening": true, "activeAddr": "[::]:8090", "mainPath": "/v1/mcp", "dedicatedPath": "/mcp",
			"mainUrl": "https://mail-api.example.com/v1/mcp", "canChange": true,
			"toolsets": []any{map[string]any{"id": "curated", "tools": 11}, map[string]any{"id": "messages", "tools": 14}},
		}}}}},
		Tags: []string{"mcp-server"}, Extensions: httpapi.WithScope(ext, "settings:read"),
	}, c.get)
	httpapi.Register(srv, huma.Operation{
		OperationID: "putMCPServer", Method: http.MethodPut, Path: "/v1/mcp/server",
		Summary: "Set the MCP listener port",
		Description: "Sets the address of the dedicated MCP listener (`8090`, `:8090` or `127.0.0.1:8090`; empty " +
			"string = off) and starts, moves or stops it on every api node without a restart. The listener serves only " +
			"`/mcp` with the same authentication as the main API; put TLS in front of it with the reverse proxy and " +
			"bind to `127.0.0.1` when the proxy runs on the same host. A bind failure (port in use, privileged port) " +
			"is returned in `error` with 200: the setting is stored and the node retries every minute. Instance " +
			"owners only (`403 forbidden` otherwise).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "Your role does not allow this operation."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The MCP port must be between 1 and 65535."}),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{"listen": "8090"}}}},
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"listen": ":8090", "listening": true, "activeAddr": "[::]:8090", "mainPath": "/v1/mcp", "dedicatedPath": "/mcp",
			"canChange": true, "toolsets": []any{map[string]any{"id": "curated", "tools": 11}},
		}}}}},
		Tags: []string{"mcp-server"}, Extensions: httpapi.WithScope(ext, "instance.admin"),
	}, c.put)
}
