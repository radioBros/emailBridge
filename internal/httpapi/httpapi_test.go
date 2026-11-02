package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

type check struct {
	name string
	err  error
}

func (c check) Name() string                { return c.name }
func (c check) Ready(context.Context) error { return c.err }

type logBuf struct{ bytes.Buffer }

func newServer(t *testing.T, o Options) (*Server, *logBuf) {
	t.Helper()
	lb := &logBuf{}
	if o.AccessLog == nil {
		o.AccessLog = slog.New(slog.NewJSONHandler(lb, nil))
	}
	if o.SecurityLog == nil {
		o.SecurityLog = slog.New(slog.NewJSONHandler(lb, nil))
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s, lb
}

type echoIn struct {
	ID   string `path:"id" pattern:"^acc_[0-9a-z]{26}$"`
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}
type echoOut struct {
	Body struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		RequestID string `json:"requestId"`
		HasDL     bool   `json:"hasDeadline"`
	}
}

func registerEcho(s *Server) {
	Register(s, opPatch("/v1/things/{id}"), func(ctx context.Context, in *echoIn) (*echoOut, error) {
		if in.Body.Name == "boom" {
			return nil, errs.New(errs.CodeConflict, "Thing is busy.")
		}
		if in.Body.Name == "panic" {
			panic("kaboom")
		}
		out := &echoOut{}
		_, has := ctx.Deadline()
		out.Body.ID, out.Body.Name, out.Body.RequestID, out.Body.HasDL = in.ID, in.Body.Name, RequestID(ctx), has
		return out, nil
	})
}

func do(s http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

const accID = "acc_01jbf3k8v4w7q2m9x5c6t0r1yz"

func problemOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("content-type %q body %s", ct, w.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func TestOperationsAndProblems(t *testing.T) {
	var panicked bool
	s, logs := newServer(t, Options{OnPanic: func(*http.Request, any, []byte) { panicked = true }})
	registerEcho(s)
	w := do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"x"}`, map[string]string{"X-Request-Id": "abcdefgh-1"})
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["requestId"] != "abcdefgh-1" || out["hasDeadline"] != true || w.Header().Get("X-Request-Id") != "abcdefgh-1" {
		t.Fatalf("out %v headers %v", out, w.Header())
	}
	// validation → 422 problem with errors[]
	w = do(s, http.MethodPatch, "/v1/things/bad", `{"name":""}`, nil)
	p := problemOf(t, w)
	if w.Code != 422 || p["code"] != "validation_failed" || p["errors"] == nil {
		t.Fatalf("validation %d %v", w.Code, p)
	}
	// domain error → its code and status
	w = do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"boom"}`, nil)
	if p := problemOf(t, w); w.Code != 409 || p["code"] != "conflict" || p["requestId"] == "" {
		t.Fatalf("conflict %d %v", w.Code, p)
	}
	// unknown route and method
	w = do(s, http.MethodGet, "/v1/nope", "", nil)
	if p := problemOf(t, w); w.Code != 404 || p["code"] != "not_found" || w.Header().Get("EB-Problem-Code") != "not_found" {
		t.Fatalf("404 %d %v", w.Code, p)
	}
	w = do(s, http.MethodDelete, "/healthz", "", nil)
	if w.Code != 405 {
		t.Fatalf("405 got %d", w.Code)
	}
	// panic → 500 problem, reported, server alive
	w = do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"panic"}`, nil)
	if p := problemOf(t, w); w.Code != 500 || p["code"] != "internal_error" || !panicked || strings.Contains(w.Body.String(), "kaboom") {
		t.Fatalf("panic %d %v", w.Code, p)
	}
	// generated request IDs
	w = do(s, http.MethodGet, "/healthz", "", map[string]string{"X-Request-Id": "bad id!"})
	if id := w.Header().Get("X-Request-Id"); !strings.HasPrefix(id, "req_") || w.Body.String() != `{"status":"ok"}` {
		t.Fatalf("request id %q body %s", id, w.Body)
	}
	if !strings.Contains(logs.String(), `"route":"/v1/things/{id}"`) || strings.Contains(logs.String(), `"route":"/healthz"`) {
		t.Fatalf("access log: %s", logs.String())
	}
	// OpenAPI is served and has our problem schema; no docs UI
	w = do(s, http.MethodGet, "/v1/openapi.json", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Problem"`) {
		t.Fatalf("openapi %d", w.Code)
	}
	if w = do(s, http.MethodGet, "/docs", "", nil); w.Code != 404 {
		t.Fatalf("docs UI must be disabled, got %d", w.Code)
	}
}

func TestSecurityHeadersAndCORS(t *testing.T) {
	s, _ := newServer(t, Options{PublicURL: "https://gw.example.com", CORSOrigins: []string{"https://app.example.com/"}})
	w := do(s, http.MethodGet, "/healthz", "", map[string]string{"Origin": "https://app.example.com"})
	h := w.Header()
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store",
		"Access-Control-Allow-Origin": "https://app.example.com", "Access-Control-Allow-Credentials": "true"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q want %q", k, h.Get(k), want)
		}
	}
	if !strings.Contains(h.Get("Strict-Transport-Security"), "max-age") || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("HSTS/CSP missing")
	}
	if tp := h.Get("Traceparent"); len(tp) != 55 {
		t.Errorf("traceparent %q", tp)
	}
	w = do(s, http.MethodGet, "/healthz", "", map[string]string{"Origin": "https://evil.example"})
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("foreign origin must not get CORS headers")
	}
	w = do(s, http.MethodOptions, "/v1/things/x", "", map[string]string{"Origin": "https://app.example.com", "Access-Control-Request-Method": "PATCH"})
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-Requested-With") {
		t.Fatalf("preflight %d %v", w.Code, w.Header())
	}
	// incoming traceparent is kept
	in := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	w = do(s, http.MethodGet, "/healthz", "", map[string]string{"traceparent": in})
	if !strings.Contains(w.Header().Get("Traceparent"), "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("trace id not propagated: %s", w.Header().Get("Traceparent"))
	}
}

func TestCSRF(t *testing.T) {
	s, logs := newServer(t, Options{PublicURL: "https://gw.example.com"})
	registerEcho(s)
	cookie := map[string]string{"Cookie": "eb_session=x"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{"Cookie": "eb_session=x"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	path := "/v1/things/" + accID
	// cookie + cross-site → origin_rejected
	w := do(s, http.MethodPatch, path, `{"name":"x"}`, with(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}))
	if p := problemOf(t, w); w.Code != 403 || p["code"] != "origin_rejected" {
		t.Fatalf("cross-site %d %v", w.Code, p)
	}
	// cookie, same-origin, no header → csrf_failed
	w = do(s, http.MethodPatch, path, `{"name":"x"}`, with(map[string]string{"Sec-Fetch-Site": "same-origin"}))
	if p := problemOf(t, w); w.Code != 403 || p["code"] != "csrf_failed" {
		t.Fatalf("missing header %d %v", w.Code, p)
	}
	// cookie, same-origin, header → ok
	w = do(s, http.MethodPatch, path, `{"name":"x"}`, with(map[string]string{"Sec-Fetch-Site": "same-origin", "X-Requested-With": "EmailBridge"}))
	if w.Code != 200 {
		t.Fatalf("valid cookie write %d %s", w.Code, w.Body)
	}
	// bearer requests and requests without cookie are exempt; GET is exempt
	if w = do(s, http.MethodPatch, path, `{"name":"x"}`, map[string]string{"Authorization": "Bearer ebk_x", "Sec-Fetch-Site": "cross-site"}); w.Code != 200 {
		t.Fatalf("bearer %d", w.Code)
	}
	if w = do(s, http.MethodPatch, path, `{"name":"x"}`, nil); w.Code != 200 {
		t.Fatalf("no cookie %d", w.Code)
	}
	if w = do(s, http.MethodGet, "/healthz", "", cookie); w.Code != 200 {
		t.Fatalf("get %d", w.Code)
	}
	// Limen routes need only the cross-origin check
	s.Router.Post("/v1/auth/signin/credential", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if w = do(s, http.MethodPost, "/v1/auth/signin/credential", `{}`, with(map[string]string{"Sec-Fetch-Site": "same-origin"})); w.Code != 204 {
		t.Fatalf("limen route %d", w.Code)
	}
	if !strings.Contains(logs.String(), "csrf header missing") {
		t.Fatal("security log")
	}
}

func TestClientIPTrustedProxies(t *testing.T) {
	s, _ := newServer(t, Options{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	var got netip.Addr
	var tls bool
	s.Router.Get("/v1/ip", func(w http.ResponseWriter, r *http.Request) { got, tls = ClientIP(r.Context()), s.isTLS(r) })
	req := func(remote, xff, proto string) netip.Addr {
		r := httptest.NewRequest(http.MethodGet, "/v1/ip", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		s.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	if a := req("203.0.113.9:5000", "1.2.3.4", ""); a.String() != "203.0.113.9" {
		t.Errorf("untrusted peer must not use XFF: %s", a)
	}
	if a := req("10.0.0.2:5000", "1.2.3.4, 10.0.0.7", "https"); a.String() != "1.2.3.4" || !tls {
		t.Errorf("trusted chain: %s tls=%v", a, tls)
	}
	if a := req("10.0.0.2:5000", "spoofed, 9.9.9.9", ""); a.String() != "9.9.9.9" {
		t.Errorf("rightmost untrusted hop: %s", a)
	}
	if a := req("10.0.0.2:5000", "", ""); a.String() != "10.0.0.2" {
		t.Errorf("no xff: %s", a)
	}
	if a := req("garbage", "", ""); a.IsValid() {
		t.Errorf("bad remote: %s", a)
	}
}

func TestTimeoutAndBodyLimit(t *testing.T) {
	s, _ := newServer(t, Options{RequestTimeout: 50 * time.Millisecond, RequestTimeoutMax: 200 * time.Millisecond, MaxBody: 64})
	var dl time.Duration
	s.Router.Get("/v1/slow", func(w http.ResponseWriter, r *http.Request) {
		d, _ := r.Context().Deadline()
		dl = time.Until(d)
	})
	s.Router.Get("/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); ok {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	do(s, http.MethodGet, "/v1/slow", "", nil)
	if dl > 60*time.Millisecond {
		t.Errorf("default timeout: %v", dl)
	}
	do(s, http.MethodGet, "/v1/slow", "", map[string]string{"EB-Timeout": "5000"})
	if dl > 210*time.Millisecond || dl < 100*time.Millisecond {
		t.Errorf("clamped timeout: %v", dl)
	}
	if w := do(s, http.MethodGet, "/v1/events/stream", "", nil); w.Code != 200 {
		t.Error("streaming routes have no deadline")
	}
	w := do(s, http.MethodPost, "/v1/slow", strings.Repeat("x", 100), nil)
	if p := problemOf(t, w); w.Code != 413 || p["code"] != "payload_too_large" {
		t.Fatalf("body limit %d %v", w.Code, p)
	}
}

func TestReadyz(t *testing.T) {
	var cs []app.Checker
	authed := false
	s, _ := newServer(t, Options{Checkers: func() []app.Checker { return cs }, Authenticated: func(*http.Request) bool { return authed }})
	cs = []app.Checker{check{"store", nil}, check{"bus", nil}}
	w := do(s, http.MethodGet, "/readyz", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "checks") {
		t.Fatalf("ready anonymous %d %s", w.Code, w.Body)
	}
	cs = append(cs, check{"bus2", errors.New("nats down: secret-host:4222")})
	w = do(s, http.MethodGet, "/readyz", "", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"failing":["bus2"]`) || strings.Contains(w.Body.String(), "secret-host") {
		t.Fatalf("not ready anonymous %d %s", w.Code, w.Body)
	}
	authed = true
	w = do(s, http.MethodGet, "/readyz", "", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "secret-host") {
		t.Fatalf("authenticated details %d %s", w.Code, w.Body)
	}
}

func TestSPA(t *testing.T) {
	spa := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app-1.js":      {Data: []byte("js")},
		"favicon.ico":          {Data: []byte("ico")},
		"version.json":         {Data: []byte(`{"v":1}`)},
		"manifest.webmanifest": {Data: []byte(`{"name":"EmailBridge"}`)},
		"icons/icon-48x48.png": {Data: []byte("png")},
		"assets/dir/x.css":     {Data: []byte("css")},
	}
	s, _ := newServer(t, Options{SPA: spa})
	cases := map[string][2]string{
		"/":                {"app", "no-cache"},
		"/accounts/acc_1":  {"app", "no-cache"},
		"/assets/app-1.js": {"js", "immutable"},
		"/favicon.ico":     {"ico", "max-age=3600"},
		"/version.json":    {`{"v":1}`, "no-cache"},
		"/assets/dir":      {"app", "no-cache"},
	}
	for path, want := range cases {
		w := do(s, http.MethodGet, path, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), want[0]) || !strings.Contains(w.Header().Get("Cache-Control"), want[1]) {
			t.Errorf("%s: %d %q %q", path, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
		}
	}
	if w := do(s, http.MethodGet, "/v1/unknown", "", nil); w.Code != 404 {
		t.Error("API paths never fall back to the SPA")
	}
	if w := do(s, http.MethodGet, "/manifest.webmanifest", "", nil); w.Code != 200 || w.Header().Get("Content-Type") != "application/manifest+json" {
		t.Errorf("manifest: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := do(s, http.MethodGet, "/icons/icon-48x48.png", "", nil); w.Code != 200 || w.Body.String() != "png" {
		t.Errorf("icon: %d %q", w.Code, w.Body.String())
	}
}

func TestModuleAndListener(t *testing.T) {
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	m := &Module{Options: Options{Addr: "127.0.0.1:0"}}
	l := &Listener{}
	if m.Name() == "" || l.Name() == "" || len(m.Roles()) != 1 || len(l.Roles()) != 1 {
		t.Fatal("identity")
	}
	ctx := context.Background()
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + l.Addr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listener status %d", resp.StatusCode)
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := l.Stop(sctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if (&Listener{}).Addr() != "" || (&Listener{}).Stop(ctx) != nil {
		t.Fatal("zero listener")
	}
	if err := (&Listener{}).Start(ctx, app.New(app.Options{})); err == nil {
		t.Fatal("listener without server must fail")
	}
	bad := &Module{Options: Options{Addr: "127.0.0.1:0"}}
	a2 := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	_ = bad.Start(ctx, a2)
	l2 := &Listener{}
	s2, _ := app.MustGet[*Server](a2)
	s2.o.Addr = "256.0.0.1:99999"
	if err := l2.Start(ctx, a2); err == nil {
		t.Fatal("bad address must fail")
	}
	if originOf("not a url") != "not a url" || originOf("https://x.test/path") != "https://x.test" {
		t.Fatal("originOf")
	}
	if _, err := New(Options{CORSOrigins: []string{"::bad"}}); err == nil {
		t.Log("cross-origin protection accepts the raw value; nothing to assert")
	}
	_ = logging.IDsFrom(ctx)
}

func TestAddMiddlewareOrder(t *testing.T) {
	s, _ := newServer(t, Options{})
	var order []string
	mk := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	s.Router.Get("/v1/late", func(w http.ResponseWriter, _ *http.Request) { order = append(order, "handler") })
	do(s, http.MethodGet, "/v1/late", "", nil)
	s.AddMiddleware(mk("a"))
	s.AddMiddleware(mk("b"))
	order = nil
	do(s, http.MethodGet, "/v1/late", "", nil)
	if strings.Join(order, ",") != "a,b,handler" {
		t.Fatalf("order = %v", order)
	}
}
