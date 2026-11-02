package mcp

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchMCP = "mcp.enabled"

// Handler serves the MCP endpoint (streamable HTTP) behind auth + feature gates: /mcp and /v1/mcp on the
// main listener, /mcp on the dedicated listener (mcp.listen).
type Handler struct {
	Gateway  *Gateway
	Settings EffectiveSettings
	Registry *features.Registry
	Env      string
	// ForceEnabled skips switch resolution when true (tests).
	ForceEnabled bool
	// OpenAPI returns the API document the generated tools come from (nil → curated tools only).
	OpenAPI func() *huma.OpenAPI
	server  *sdkmcp.Server
	http    *sdkmcp.StreamableHTTPHandler

	genOnce sync.Once
	gen     []*genTool
	mu      sync.Mutex
	servers map[string]*sdkmcp.Server
}

// Mount registers /mcp and /v1/mcp on the chi router.
func (h *Handler) Mount(srv *httpapi.Server) {
	h.server = NewServer(h.Gateway)
	if h.Gateway != nil && h.Gateway.API == nil {
		h.Gateway.API = srv
	}
	if h.OpenAPI == nil {
		h.OpenAPI = srv.API.OpenAPI
	}
	h.http = sdkmcp.NewStreamableHTTPHandler(h.serverFor, nil)
	for _, p := range []string{"/mcp", "/mcp/", "/v1/mcp", "/v1/mcp/"} {
		srv.Router.Handle(p, h)
	}
	h.document(srv)
}

// generated builds the generated tools once, on first use: every module has registered its operations by
// the time the first MCP request arrives.
func (h *Handler) generated() []*genTool {
	h.genOnce.Do(func() {
		if h.OpenAPI != nil {
			h.gen = GenerateTools(h.OpenAPI(), curatedToolNames)
		}
	})
	return h.gen
}

// parseToolsets reads ?toolsets=a,b (nil = all; error on an unknown name).
func parseToolsets(r *http.Request) (map[string]bool, string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("toolsets"))
	if raw == "" || raw == "all" {
		return nil, "", nil
	}
	known := map[string]bool{}
	for _, t := range AllToolsets {
		known[t] = true
	}
	sets := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		if p == "all" {
			return nil, "", nil
		}
		if !known[p] {
			return nil, "", errs.New(errs.CodeValidationFailed, "Unknown MCP toolset.").WithSafe("toolset", p).
				WithSafe("known", strings.Join(AllToolsets, ","))
		}
		sets[p] = true
	}
	if len(sets) == 0 {
		return nil, "", nil
	}
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return sets, strings.Join(keys, ","), nil
}

// serverFor picks (and caches) the MCP server of the request's toolsets.
func (h *Handler) serverFor(r *http.Request) *sdkmcp.Server {
	sets, key, err := parseToolsets(r)
	if err != nil {
		return h.server // unreachable: ServeHTTP refused the request already
	}
	if h.OpenAPI == nil && sets == nil {
		return h.server
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.servers == nil {
		h.servers = map[string]*sdkmcp.Server{}
	}
	if s, ok := h.servers[key]; ok {
		return s
	}
	s := newToolsetServer(h.Gateway, h.generated(), sets)
	h.servers[key] = s
	return s
}

// ToolsetCounts returns the number of tools per toolset (curated included).
func (h *Handler) ToolsetCounts() map[string]int {
	out := map[string]int{ToolsetCurated: len(curatedToolNames)}
	for _, t := range h.generated() {
		out[t.Toolset]++
	}
	return out
}

// ServeHTTP gates then delegates to the streamable transport.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeUnauthenticated, "Authentication is required."))
		return
	}
	if c.Kind != tenancy.CallerAPIKey {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeForbidden, "MCP requires an API key."))
		return
	}
	if !hasScope(c.Scopes, "mcp:use") {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeScopeMissing, "The API key lacks mcp:use.").WithSafe("scope", "mcp:use"))
		return
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		org = c.OrganizationID
	}
	if !h.mcpEnabled(ctx, org) {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeFeatureDisabled, "The MCP endpoint is disabled for this organisation."))
		return
	}
	if _, _, err := parseToolsets(r); err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	// Generated tools replay the call through the API: they need the peer address (IP allow-lists, rate
	// limits) and the request ID. Always overwritten, so a client cannot choose them.
	r.Header.Set(hdrPeer, r.RemoteAddr)
	if id := logging.IDsFrom(ctx).RequestID; id != "" {
		r.Header.Set("X-Request-Id", id)
	}
	h.http.ServeHTTP(w, r)
}

func (h *Handler) mcpEnabled(ctx context.Context, org string) bool {
	if h.ForceEnabled {
		return true
	}
	sw, ok := h.registry().Get(switchMCP)
	if !ok {
		return false
	}
	vals := features.Values{}
	if h.Settings != nil {
		if body, err := h.Settings.Effective(ctx, org); err == nil {
			if sv, exists := body.Switches[switchMCP]; exists {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	}
	env := h.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return false
}

func (h *Handler) registry() *features.Registry {
	if h.Registry != nil {
		return h.Registry
	}
	return features.DefaultRegistry()
}

// EffectiveSettings is the subset of settings.Service used for PII options.
type EffectiveSettings interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
}

// ResolvePIISettings reads org MCP PII settings (defaults: redact on, 4000 chars).
func ResolvePIISettings(ctx context.Context, st EffectiveSettings) (redact bool, maxChars int) {
	redact, maxChars = true, 4000
	if st == nil {
		return
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return
	}
	body, err := st.Effective(ctx, org)
	if err != nil {
		return
	}
	if e, ok := body.Values["mcp.piiRedact"]; ok {
		if b, ok := e.Value.(bool); ok {
			redact = b
		}
	}
	if e, ok := body.Values["mcp.bodyMaxChars"]; ok {
		switch n := e.Value.(type) {
		case int:
			if n > 0 {
				maxChars = n
			}
		case int64:
			if n > 0 {
				maxChars = int(n)
			}
		case float64:
			if n > 0 {
				maxChars = int(n)
			}
		}
	}
	return
}
