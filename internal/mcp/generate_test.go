package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestSnakeName(t *testing.T) {
	for in, want := range map[string]string{
		"getAISettings": "get_ai_settings", "listAccounts": "list_accounts", "putMCPServer": "put_mcp_server",
		"aiSummarizeMessage": "ai_summarize_message", "getAccountV2Stats": "get_account_v2_stats",
		"list-webhook endpoints": "list_webhook_endpoints", "URLCheck": "url_check",
		strings.Repeat("a", 80): strings.Repeat("a", 64),
	} {
		if got := snakeName(in); got != want {
			t.Errorf("snakeName(%q) = %q, want %q", in, got, want)
		}
	}
}

type thingBody struct {
	Name  string   `json:"name" doc:"Name of the thing"`
	Inner *innerT  `json:"inner,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

type innerT struct {
	Level int `json:"level"`
}

type thingOut struct {
	Body struct {
		ID     string `json:"id"`
		Name   string `json:"name,omitempty"`
		Peer   string `json:"peer,omitempty"`
		Filter string `json:"filter,omitempty"`
		Owner  string `json:"owner,omitempty"`
	}
}

// testAPI registers a few operations shaped like the real ones and a stub authentication that turns
// "Bearer read" / "Bearer write" into API-key callers.
func testAPI(t *testing.T) *httpapi.Server {
	t.Helper()
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	srv.AddMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var scopes []string
			switch r.Header.Get("Authorization") {
			case "Bearer read":
				scopes = []string{"mcp:use", "accounts:read"}
			case "Bearer write":
				scopes = []string{"mcp:use", "accounts:write"}
			default:
				next.ServeHTTP(w, r)
				return
			}
			ctx := store.WithOrganization(r.Context(), "org_test")
			ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_test", OrganizationID: "org_test", Scopes: scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	ext := func(scope string) map[string]any { return httpapi.WithScope(httpapi.Phase("P10"), scope) }
	httpapi.Register(srv, huma.Operation{
		OperationID: "getThing", Method: http.MethodGet, Path: "/v1/things/{thingId}", Summary: "Get a thing",
		Description: "Returns one thing.", Tags: []string{"accounts"}, Extensions: ext("accounts:read"),
	}, func(ctx context.Context, in *struct {
		ThingID string   `path:"thingId" doc:"Thing ID"`
		Filter  []string `query:"filter"`
	}) (*thingOut, error) {
		if err := tenancy.RequireRead(ctx, "accounts"); err != nil {
			return nil, err
		}
		out := &thingOut{}
		out.Body.ID = in.ThingID
		out.Body.Peer = httpapi.ClientIP(ctx).String()
		out.Body.Filter = strings.Join(in.Filter, "|")
		out.Body.Owner = "giulia.bianchi@example.org"
		return out, nil
	})
	httpapi.Register(srv, huma.Operation{
		OperationID: "createThing", Method: http.MethodPost, Path: "/v1/things", Summary: "Create a thing",
		Tags: []string{"messages"}, Extensions: ext("accounts:write"),
	}, func(ctx context.Context, in *struct{ Body thingBody }) (*thingOut, error) {
		if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
			return nil, err
		}
		out := &thingOut{}
		out.Body.ID, out.Body.Name = "thg_1", in.Body.Name
		return out, nil
	})
	httpapi.Register(srv, huma.Operation{
		OperationID: "bigThing", Method: http.MethodGet, Path: "/v1/things-big", Summary: "A big list",
		Tags: []string{"unknown-tag"}, Extensions: ext("accounts:read"),
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []string }, error) {
		out := make([]string, 9000)
		for i := range out {
			out[i] = "0123456789"
		}
		return &struct{ Body []string }{Body: out}, nil
	})
	// Not tools: public, streaming, MCP itself, no scope, a curated name.
	httpapi.Register(srv, huma.Operation{
		OperationID: "publicThing", Method: http.MethodGet, Path: "/v1/public-thing", Summary: "Public",
		Security: []map[string][]string{}, Extensions: ext("accounts:read"),
	}, func(context.Context, *struct{}) (*thingOut, error) { return &thingOut{}, nil })
	httpapi.Register(srv, huma.Operation{
		OperationID: "noScope", Method: http.MethodGet, Path: "/v1/no-scope", Summary: "No scope",
	}, func(context.Context, *struct{}) (*thingOut, error) { return &thingOut{}, nil })
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccounts", Method: http.MethodGet, Path: "/v1/accounts-x", Summary: "Collides",
		Extensions: ext("accounts:read"),
	}, func(context.Context, *struct{}) (*thingOut, error) { return &thingOut{}, nil })
	srv.Document(&huma.Operation{
		OperationID: "streamThings", Method: http.MethodGet, Path: "/v1/things/stream", Summary: "Stream",
		Responses:  map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"text/event-stream": {}}}},
		Extensions: ext("accounts:read"),
	})
	return srv
}

func toolByName(gen []*genTool, name string) *genTool {
	for _, g := range gen {
		if g.Name == name {
			return g
		}
	}
	return nil
}

func TestGenerateToolsFromOpenAPI(t *testing.T) {
	srv := testAPI(t)
	h := &Handler{Gateway: &Gateway{}, ForceEnabled: true}
	h.Mount(srv)
	gen := GenerateTools(srv.API.OpenAPI(), curatedToolNames)
	var names []string
	for _, g := range gen {
		names = append(names, g.Name+"@"+g.Toolset)
	}
	got := strings.Join(names, ",")
	if got != "big_thing@system,create_thing@messages,get_thing@accounts" {
		t.Fatalf("generated %s", got)
	}
	get := toolByName(gen, "get_thing")
	raw, _ := json.Marshal(get.Tool.InputSchema)
	s := string(raw)
	if !strings.Contains(s, `"required":["thingId"]`) || !strings.Contains(s, `"filter"`) || !get.Tool.Annotations.ReadOnlyHint {
		t.Fatalf("get schema %s", s)
	}
	if !strings.Contains(get.Tool.Description, "REST: GET /v1/things/{thingId}") {
		t.Fatalf("description %q", get.Tool.Description)
	}
	create := toolByName(gen, "create_thing")
	raw, _ = json.Marshal(create.Tool.InputSchema)
	s = string(raw)
	if strings.Contains(s, "$ref") || !strings.Contains(s, `"level"`) || !strings.Contains(s, `"body"`) {
		t.Fatalf("body schema must be inlined: %s", s)
	}
}

func TestParseToolsets(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp?toolsets=Messages,%20curated", nil)
	sets, key, err := parseToolsets(r)
	if err != nil || key != "curated,messages" || !sets["messages"] {
		t.Fatalf("%v %q %v", sets, key, err)
	}
	for _, q := range []string{"", "all", "messages,all"} {
		sets, _, err = parseToolsets(httptest.NewRequest(http.MethodPost, "/mcp?toolsets="+q, nil))
		if err != nil || sets != nil {
			t.Fatalf("%q: %v %v", q, sets, err)
		}
	}
	if _, _, err := parseToolsets(httptest.NewRequest(http.MethodPost, "/mcp?toolsets=nope", nil)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) *sdkmcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := cli.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearerRT{token}, Timeout: 10 * time.Second}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func toolNames(t *testing.T, sess *sdkmcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := sess.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range res.Tools {
		out[tl.Name] = true
	}
	return out
}

func callText(t *testing.T, sess *sdkmcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Content[0].(*sdkmcp.TextContent).Text, res.IsError
}

func TestGeneratedToolsOverHTTP(t *testing.T) {
	srv := testAPI(t)
	h := &Handler{Gateway: &Gateway{ResolvePII: func(context.Context) (bool, int) { return true, 4000 }}, ForceEnabled: true}
	h.Mount(srv)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	sess := connect(t, ts.URL+"/v1/mcp", "read")
	names := toolNames(t, sess)
	for _, want := range []string{"list_accounts", "send_message", "get_thing", "create_thing", "big_thing"} {
		if !names[want] {
			t.Fatalf("tools/list misses %s: %v", want, names)
		}
	}
	text, isErr := callText(t, sess, "get_thing", map[string]any{"thingId": "thg 7", "filter": []any{"a", "b"}})
	if isErr || !strings.Contains(text, `"id":"thg 7"`) || !strings.Contains(text, `"filter":"a|b"`) {
		t.Fatalf("get_thing %v %s", isErr, text)
	}
	if !strings.Contains(text, `"peer":"127.0.0.1"`) {
		t.Fatalf("the caller's address must reach the API: %s", text)
	}
	if strings.Contains(text, "giulia.bianchi@example.org") || !strings.Contains(text, "addr_") {
		t.Fatalf("mcp.piiRedact applies to generated tools: %s", text)
	}
	// The read key cannot write: the API's own problem comes back as a tool error.
	text, isErr = callText(t, sess, "create_thing", map[string]any{"body": map[string]any{"name": "x"}})
	if !isErr || !strings.Contains(text, "scope_missing") {
		t.Fatalf("create with read key: %v %s", isErr, text)
	}
	text, isErr = callText(t, sess, "get_thing", map[string]any{})
	if !isErr || !strings.Contains(text, "thingId is required") {
		t.Fatalf("missing path param: %v %s", isErr, text)
	}
	// Redaction is on: the big result is still cut at 64 KiB, not at mcp.bodyMaxChars.
	text, isErr = callText(t, sess, "big_thing", nil)
	if isErr || !strings.Contains(text, "[truncated:") || len(text) < 60<<10 || len(text) > maxToolResultSize+200 {
		t.Fatalf("big: %v len %d", isErr, len(text))
	}

	wsess := connect(t, ts.URL+"/mcp", "write")
	text, isErr = callText(t, wsess, "create_thing", map[string]any{"body": map[string]any{"name": "Invoice box", "inner": map[string]any{"level": 2}}})
	if isErr || !strings.Contains(text, `"name":"Invoice box"`) {
		t.Fatalf("create with write key: %v %s", isErr, text)
	}

	// Toolsets narrow tools/list.
	only := connect(t, ts.URL+"/mcp?toolsets=accounts", "read")
	names = toolNames(t, only)
	if !names["get_thing"] || names["create_thing"] || names["list_accounts"] {
		t.Fatalf("toolsets=accounts: %v", names)
	}
	cur := connect(t, ts.URL+"/mcp?toolsets=curated", "read")
	names = toolNames(t, cur)
	if names["get_thing"] || !names["list_accounts"] {
		t.Fatalf("toolsets=curated: %v", names)
	}
	// Unknown toolset: refused before the MCP transport.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp?toolsets=bogus", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer read")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity && res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown toolset status %d", res.StatusCode)
	}
	counts := h.ToolsetCounts()
	if counts[ToolsetCurated] != len(curatedToolNames) || counts[ToolsetAccounts] != 1 {
		t.Fatalf("counts %v", counts)
	}
}

func TestPeerHeaderCannotBeSpoofed(t *testing.T) {
	srv := testAPI(t)
	h := &Handler{Gateway: &Gateway{}, ForceEnabled: true}
	h.Mount(srv)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := cli.Connect(context.Background(), &sdkmcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: spoofRT{}, Timeout: 10 * time.Second},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	text, _ := callText(t, sess, "get_thing", map[string]any{"thingId": "x"})
	if !strings.Contains(text, `"peer":"127.0.0.1"`) {
		t.Fatalf("spoofed peer accepted: %s", text)
	}
}

type spoofRT struct{}

func (spoofRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer read")
	r.Header.Set(hdrPeer, "203.0.113.9:4444")
	return http.DefaultTransport.RoundTrip(r)
}

func TestNormalizeListen(t *testing.T) {
	for in, want := range map[string]string{
		"": "", " 8090 ": ":8090", ":8090": ":8090", "127.0.0.1:8090": "127.0.0.1:8090", "[::1]:9000": "[::1]:9000",
		"localhost:0": "localhost:0",
	} {
		got, err := NormalizeListen(in)
		if err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"70000", "abc", "host:port", "1.2.3.4", "a b:80"} {
		if _, err := NormalizeListen(bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("%q accepted", bad)
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) int {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get(url)
	if err != nil {
		return 0
	}
	_ = res.Body.Close()
	return res.StatusCode
}

func TestListenerStartMoveStop(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	l := &Listener{Handler: ok}
	ctx := context.Background()
	first := freePort(t)
	st := l.Apply(ctx, first)
	if st.Active != first || st.Error != "" {
		t.Fatalf("start %+v", st)
	}
	if code := get(t, "http://"+first+"/mcp"); code != http.StatusTeapot {
		t.Fatalf("/mcp on the dedicated port: %d", code)
	}
	if code := get(t, "http://"+first+"/v1/accounts"); code != http.StatusNotFound {
		t.Fatalf("other paths must be 404: %d", code)
	}
	if again := l.Apply(ctx, first); again.Active != first {
		t.Fatal("same address must be a no-op")
	}
	second := freePort(t)
	st = l.Apply(ctx, second)
	if st.Active != second {
		t.Fatalf("move %+v", st)
	}
	if get(t, "http://"+first+"/mcp") != 0 {
		t.Fatal("old port still open after a move")
	}
	if get(t, "http://"+second+"/mcp") != http.StatusTeapot {
		t.Fatal("new port not serving")
	}
	// Bind error: the port is taken.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	st = l.Apply(ctx, busy.Addr().String())
	if st.Active != "" || st.Error == "" {
		t.Fatalf("busy port %+v", st)
	}
	if get(t, "http://"+second+"/mcp") != 0 {
		t.Fatal("previous port must be closed after a move, even a failed one")
	}
	st = l.Apply(ctx, "")
	if st.Active != "" || st.Error != "" || st.Configured != "" {
		t.Fatalf("off %+v", st)
	}
	l.Stop(ctx)
}

type fakeListenSettings struct {
	value   any
	patched map[string]any
	err     error
	inval   int
}

func (f *fakeListenSettings) Effective(context.Context, string) (settings.SettingsBody, error) {
	return settings.SettingsBody{Values: map[string]settings.SettingValue{SettingListen: {Value: f.value}}}, nil
}

func (f *fakeListenSettings) Patch(_ context.Context, scope string, patch map[string]any, _ string) (settings.SettingsBody, error) {
	if f.err != nil {
		return settings.SettingsBody{}, f.err
	}
	if scope != "global" {
		return settings.SettingsBody{}, errs.New(errs.CodeForbidden, "global only")
	}
	f.patched = patch
	f.value = patch["values"].(map[string]any)[SettingListen]
	return settings.SettingsBody{}, nil
}

func (f *fakeListenSettings) Invalidate(string) { f.inval++ }

func TestControlReconcileAndPut(t *testing.T) {
	addr := freePort(t)
	fs := &fakeListenSettings{value: addr}
	ctl := &Control{Settings: fs, Listener: &Listener{Handler: http.NotFoundHandler()}, PublicURL: "https://mail.example.com/"}
	defer ctl.Listener.Stop(context.Background())
	if st := ctl.Reconcile(context.Background()); st.Active != "" {
		t.Fatalf("the port must stay closed before Activate: %+v", st)
	}
	if st := ctl.Activate(context.Background()); st.Active != addr {
		t.Fatalf("activate %+v", st)
	}
	ownerCtx := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerUser, InstanceOwner: true, Role: tenancy.RoleOwner})
	out, err := ctl.put(ownerCtx, &serverIn{Body: ServerPut{Listen: ""}})
	if err != nil || out.Body.Listening || out.Body.Listen != "" || fs.value != nil {
		t.Fatalf("off: %+v %v %v", out, err, fs.value)
	}
	if !out.Body.CanChange || out.Body.MainURL != "https://mail.example.com/v1/mcp" || out.Body.MainPath != "/v1/mcp" {
		t.Fatalf("view %+v", out.Body)
	}
	if _, err := ctl.put(ownerCtx, &serverIn{Body: ServerPut{Listen: "99999"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad port: %v", err)
	}
	fs.err = errs.New(errs.CodeForbidden, "Your role does not allow this operation.")
	if _, err := ctl.put(ownerCtx, &serverIn{Body: ServerPut{Listen: "8090"}}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("forbidden: %v", err)
	}
	fs.err = nil
	fs.value = "not an address"
	if st := ctl.Reconcile(context.Background()); st.Error == "" || st.Active != "" {
		t.Fatalf("stored garbage: %+v", st)
	}
	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerAPIKey, Scopes: []string{"settings:read"}})
	if _, err := ctl.put(viewer, &serverIn{Body: ServerPut{Listen: "bad"}}); errs.CodeOf(err) == errs.CodeValidationFailed || err == nil {
		t.Fatalf("permission comes before validation: %v", err)
	}
	g, err := ctl.get(viewer, nil)
	if err != nil || g.Body.CanChange {
		t.Fatalf("get %+v %v", g, err)
	}
	if _, err := ctl.get(context.Background(), nil); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("anonymous get: %v", err)
	}
}
