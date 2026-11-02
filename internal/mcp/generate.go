package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Toolsets group the tools; `?toolsets=a,b` on the MCP URL selects some (default: all).
const (
	ToolsetCurated      = "curated"
	ToolsetAccounts     = "accounts"
	ToolsetMessages     = "messages"
	ToolsetSending      = "sending"
	ToolsetWebhooks     = "webhooks"
	ToolsetTemplates    = "templates"
	ToolsetEvents       = "events"
	ToolsetSearch       = "search"
	ToolsetSettings     = "settings"
	ToolsetOrganization = "organization"
	ToolsetSystem       = "system"
)

// AllToolsets lists the toolset IDs in display order.
var AllToolsets = []string{
	ToolsetCurated, ToolsetAccounts, ToolsetMessages, ToolsetSending, ToolsetWebhooks, ToolsetTemplates,
	ToolsetEvents, ToolsetSearch, ToolsetSettings, ToolsetOrganization, ToolsetSystem,
}

// tagToolsets maps OpenAPI tags to toolsets; an unknown tag lands in system.
var tagToolsets = map[string]string{
	"accounts": ToolsetAccounts, "mailboxes": ToolsetAccounts, "connect-links": ToolsetAccounts,
	"oauth-apps": ToolsetAccounts, "oauth": ToolsetAccounts, "autoconfig": ToolsetAccounts, "pec": ToolsetAccounts,
	"messages": ToolsetMessages, "drafts": ToolsetMessages, "attachments": ToolsetMessages, "exports": ToolsetMessages,
	"outbound-messages": ToolsetSending, "smtp-gateways": ToolsetSending, "gateways": ToolsetSending,
	"suppression-lists": ToolsetSending, "tracking": ToolsetSending, "sandbox": ToolsetSending, "sending": ToolsetSending,
	"webhook-endpoints": ToolsetWebhooks, "webhooks": ToolsetWebhooks, "routes": ToolsetWebhooks,
	"rule-lists": ToolsetWebhooks, "rules": ToolsetWebhooks,
	"templates":  ToolsetTemplates,
	"deliveries": ToolsetEvents, "delivery-tests": ToolsetEvents, "events": ToolsetEvents,
	"ai": ToolsetSearch, "search": ToolsetSearch, "embeddings": ToolsetSearch, "semantic-search": ToolsetSearch,
	"settings": ToolsetSettings, "config": ToolsetSettings, "integrations": ToolsetSettings, "oidc": ToolsetSettings,
	"feature-switches": ToolsetSettings,
	"organizations":    ToolsetOrganization, "members": ToolsetOrganization, "api-keys": ToolsetOrganization,
	"audit-log": ToolsetOrganization, "me": ToolsetOrganization, "invitations": ToolsetOrganization,
	"notifications": ToolsetOrganization,
	"health":        ToolsetSystem, "queues": ToolsetSystem, "cluster": ToolsetSystem, "stats": ToolsetSystem,
	"jobs": ToolsetSystem, "logs": ToolsetSystem,
}

// toolsetOfTags picks the toolset of an operation from its first mapped tag.
func toolsetOfTags(tags []string) string {
	for _, t := range tags {
		if ts, ok := tagToolsets[strings.ToLower(t)]; ok {
			return ts
		}
	}
	return ToolsetSystem
}

// skippedScopes are operations an API key can never call usefully over MCP.
var skippedScopes = map[string]bool{"mcp:use": true, "instance.admin": true}

const (
	maxToolNameLen    = 64
	maxToolDescLen    = 2000
	maxSchemaDepth    = 8
	maxToolResultSize = 64 << 10 // text returned to the client
	maxToolCapture    = 4 << 20  // body read from the API, so redaction sees whole JSON
)

// genParam is one path / query / header parameter of a generated tool.
type genParam struct {
	Name    string
	In      string
	Explode bool
}

// genTool is one API operation exposed as an MCP tool.
type genTool struct {
	Name     string
	Toolset  string
	Method   string
	Path     string
	Params   []genParam
	HasBody  bool
	Tool     *sdkmcp.Tool
	readOnly bool
}

// snakeName turns an operationId into a tool name: getAISettings → get_ai_settings.
func snakeName(id string) string {
	rs := []rune(id)
	var b strings.Builder
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			continue
		}
		if unicode.IsUpper(r) && i > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if (unicode.IsLower(prev) || unicode.IsDigit(prev)) || (unicode.IsUpper(prev) && nextLower) {
				if !strings.HasSuffix(b.String(), "_") {
					b.WriteByte('_')
				}
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > maxToolNameLen {
		out = strings.TrimRight(out[:maxToolNameLen], "_")
	}
	return out
}

// schemaResolver inlines #/components/schemas references (MCP clients expect self-contained schemas).
type schemaResolver struct {
	components map[string]any
}

func newSchemaResolver(oapi *huma.OpenAPI) *schemaResolver {
	r := &schemaResolver{components: map[string]any{}}
	if oapi == nil || oapi.Components == nil || oapi.Components.Schemas == nil {
		return r
	}
	raw, err := json.Marshal(oapi.Components.Schemas.Map())
	if err == nil {
		_ = json.Unmarshal(raw, &r.components)
	}
	return r
}

// toMap converts a huma schema to a generic JSON object with references inlined.
func (r *schemaResolver) toMap(s *huma.Schema) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return map[string]any{}
	}
	out, _ := r.resolve(m, 0, map[string]bool{}).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func (r *schemaResolver) resolve(v any, depth int, seen map[string]bool) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			target, found := r.components[name]
			if !found || seen[name] || depth >= maxSchemaDepth {
				return map[string]any{"type": "object", "description": "Nested " + name + " object (see the API reference)."}
			}
			seen[name] = true
			defer delete(seen, name)
			return r.resolve(target, depth+1, seen)
		}
		out := make(map[string]any, len(t))
		for k, x := range t {
			if k == "$schema" {
				continue
			}
			out[k] = r.resolve(x, depth+1, seen)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = r.resolve(x, depth+1, seen)
		}
		return out
	default:
		return v
	}
}

func isJSONType(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "application/json" || (strings.HasPrefix(ct, "application/") && strings.HasSuffix(ct, "+json"))
}

// skipOperation reports why an operation is not a tool ("" = it is one).
func skipOperation(path string, op *huma.Operation) string {
	switch {
	case op == nil || op.OperationID == "":
		return "no operationId"
	case op.Hidden:
		return "hidden"
	case path == "/mcp" || strings.HasPrefix(path, "/v1/mcp") || strings.HasPrefix(path, "/v1/auth/"):
		return "mcp or auth flow"
	case op.Security != nil && len(op.Security) == 0:
		return "public"
	}
	for _, t := range op.Tags {
		if strings.EqualFold(t, "mcp") || strings.EqualFold(t, "auth") {
			return "mcp or auth flow"
		}
	}
	scope, _ := op.Extensions["x-eb-scope"].(string)
	if scope == "" || skippedScopes[scope] {
		return "no API-key scope"
	}
	if op.RequestBody != nil && len(op.RequestBody.Content) > 0 {
		if _, ok := op.RequestBody.Content["application/json"]; !ok {
			return "non-JSON body"
		}
	}
	for code, res := range op.Responses {
		if res == nil || len(code) != 3 || code[0] != '2' || len(res.Content) == 0 {
			continue
		}
		jsonish := false
		for ct := range res.Content {
			if strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
				return "streaming"
			}
			if isJSONType(ct) {
				jsonish = true
			}
		}
		if !jsonish {
			return "binary"
		}
	}
	return ""
}

// GenerateTools builds one tool per API operation of the OpenAPI document. reserved names (the curated
// tools) win over generated ones with the same name.
func GenerateTools(oapi *huma.OpenAPI, reserved map[string]bool) []*genTool {
	if oapi == nil {
		return nil
	}
	res := newSchemaResolver(oapi)
	paths := make([]string, 0, len(oapi.Paths))
	for p := range oapi.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	seen := map[string]bool{}
	var out []*genTool
	for _, p := range paths {
		item := oapi.Paths[p]
		if item == nil {
			continue
		}
		for _, mo := range []struct {
			method string
			op     *huma.Operation
		}{
			{http.MethodGet, item.Get}, {http.MethodPost, item.Post}, {http.MethodPut, item.Put},
			{http.MethodPatch, item.Patch}, {http.MethodDelete, item.Delete},
		} {
			if skipOperation(p, mo.op) != "" {
				continue
			}
			name := snakeName(mo.op.OperationID)
			if name == "" || reserved[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, buildTool(res, name, mo.method, p, mo.op))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func buildTool(res *schemaResolver, name, method, path string, op *huma.Operation) *genTool {
	g := &genTool{
		Name: name, Toolset: toolsetOfTags(op.Tags), Method: method, Path: path,
		readOnly: method == http.MethodGet,
	}
	props := map[string]any{}
	var required []string
	for _, prm := range op.Parameters {
		if prm == nil || prm.Name == "" {
			continue
		}
		switch prm.In {
		case "path", "query", "header":
		default:
			continue
		}
		if prm.In == "header" && (strings.EqualFold(prm.Name, "Authorization") || strings.EqualFold(prm.Name, "Cookie")) {
			continue
		}
		if _, dup := props[prm.Name]; dup || prm.Name == "body" {
			continue
		}
		sch := res.toMap(prm.Schema)
		if prm.Description != "" {
			sch["description"] = prm.Description
		}
		props[prm.Name] = sch
		if prm.Required || prm.In == "path" {
			required = append(required, prm.Name)
		}
		g.Params = append(g.Params, genParam{Name: prm.Name, In: prm.In, Explode: prm.Explode != nil && *prm.Explode})
	}
	if op.RequestBody != nil {
		if mt, ok := op.RequestBody.Content["application/json"]; ok && mt != nil {
			body := res.toMap(mt.Schema)
			if len(body) == 0 {
				body = map[string]any{"type": "object"}
			}
			if _, has := body["description"]; !has {
				body["description"] = "JSON request body."
			}
			props["body"] = body
			g.HasBody = true
			if op.RequestBody.Required {
				required = append(required, "body")
			}
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	desc := strings.TrimSpace(op.Summary)
	if d := strings.TrimSpace(op.Description); d != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += d
	}
	desc += fmt.Sprintf("\n\nREST: %s %s.", method, path)
	if utf8.RuneCountInString(desc) > maxToolDescLen {
		desc = string([]rune(desc)[:maxToolDescLen]) + "…"
	}
	ann := &sdkmcp.ToolAnnotations{ReadOnlyHint: g.readOnly, IdempotentHint: method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete}
	if method == http.MethodDelete {
		t := true
		ann.DestructiveHint = &t
	}
	g.Tool = &sdkmcp.Tool{Name: name, Title: op.Summary, Description: desc, InputSchema: schema, Annotations: ann}
	return g
}

// Headers forwarded from the MCP request to the API request. hdrPeer carries the MCP request's peer
// address; Handler.ServeHTTP overwrites it on every request, so a client cannot choose it.
const hdrPeer = "Eb-Mcp-Peer"

var forwardedHeaders = []string{"Authorization", "Accept-Language", "X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Request-Id", "Traceparent", "Tracestate"}

// call runs the tool's operation through the server's full HTTP stack (authentication, scopes, rate
// limits, audit, problem responses) with the caller's own credentials.
func (g *Gateway) call(ctx context.Context, t *genTool, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	if g.API == nil {
		return g.textResult(ctx, t.Name, "error: the API is not available on this node", true), nil
	}
	args := map[string]any{}
	if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return g.textResult(ctx, t.Name, "error: arguments must be a JSON object", true), nil
		}
	}
	// A fresh context: values of the MCP request (chi's route context, the caller) must not leak into the
	// replayed request, which authenticates on its own; cancellation still follows the tool call.
	inner, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	httpReq, err := t.request(inner, args)
	if err != nil {
		return g.textResult(ctx, t.Name, "error: "+err.Error(), true), nil
	}
	var in http.Header
	if req != nil && req.Extra != nil {
		in = req.Extra.Header
	}
	for _, h := range forwardedHeaders {
		if v := in.Values(h); len(v) > 0 {
			httpReq.Header[http.CanonicalHeaderKey(h)] = append([]string(nil), v...)
		}
	}
	httpReq.RemoteAddr = in.Get(hdrPeer)
	if httpReq.RemoteAddr == "" {
		httpReq.RemoteAddr = "0.0.0.0:0"
	}
	rec := newCappedRecorder(maxToolCapture)
	g.API.ServeHTTP(rec, httpReq)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	text := rec.buf.String()
	if text == "" {
		text = fmt.Sprintf(`{"status":%d}`, rec.status)
	}
	truncated := rec.truncated
	if redact, max := g.pii(ctx); redact {
		if truncated {
			text = HashEmails(text) // not JSON any more: hash addresses, keep the length
		} else {
			text = RedactJSON(text, max)
		}
	}
	if len(text) > maxToolResultSize {
		text = cutUTF8(text, maxToolResultSize)
		truncated = true
	}
	if truncated {
		text += fmt.Sprintf("\n\n[truncated: the response is larger than %d KiB; narrow it with limit, filters or fields]", maxToolResultSize>>10)
	}
	return g.textResult(ctx, t.Name, text, rec.status >= 400), nil
}

func (g *Gateway) textResult(ctx context.Context, tool, text string, isErr bool) *sdkmcp.CallToolResult {
	errMsg := ""
	if isErr {
		errMsg = "tool call failed"
	}
	g.audit(ctx, tool, !isErr, errMsg)
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}, IsError: isErr}
}

// request builds the API request from tool arguments.
func (t *genTool) request(ctx context.Context, args map[string]any) (*http.Request, error) {
	path := t.Path
	q := url.Values{}
	hdr := http.Header{}
	for _, p := range t.Params {
		v, ok := args[p.Name]
		if !ok || v == nil {
			if p.In == "path" {
				return nil, fmt.Errorf("%s is required", p.Name)
			}
			continue
		}
		switch p.In {
		case "path":
			s := scalarString(v)
			if s == "" {
				return nil, fmt.Errorf("%s is required", p.Name)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(s))
		case "query":
			if arr, isArr := v.([]any); isArr {
				parts := make([]string, 0, len(arr))
				for _, x := range arr {
					parts = append(parts, scalarString(x))
				}
				if p.Explode {
					q[p.Name] = parts
				} else {
					q.Set(p.Name, strings.Join(parts, ","))
				}
				continue
			}
			q.Set(p.Name, scalarString(v))
		case "header":
			hdr.Set(p.Name, scalarString(v))
		}
	}
	var body *bytes.Reader
	if b, ok := args["body"]; ok && t.HasBody && b != nil {
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("body is not valid JSON")
		}
		body = bytes.NewReader(raw)
	}
	u := path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, t.Method, u, body)
	} else {
		req, err = http.NewRequestWithContext(ctx, t.Method, u, http.NoBody)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid arguments")
	}
	req.Header = hdr
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// cutUTF8 shortens s to at most n bytes without splitting a character.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

// cappedRecorder is a ResponseWriter that keeps at most max bytes of the body.
type cappedRecorder struct {
	hdr       http.Header
	status    int
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newCappedRecorder(max int) *cappedRecorder {
	return &cappedRecorder{hdr: http.Header{}, max: max}
}

func (c *cappedRecorder) Header() http.Header { return c.hdr }

func (c *cappedRecorder) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *cappedRecorder) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	room := c.max - c.buf.Len()
	if room <= 0 {
		c.truncated = c.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// Flush implements http.Flusher (no-op: the body is read after the handler returns).
func (c *cappedRecorder) Flush() {}
