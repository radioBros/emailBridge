package query

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
	tenant "github.com/radiobros/emailbridge/internal/store"
)

type ownerKey struct{}

// testAuthz reads the caller set by the test wrapper; a caller with neither is unauthenticated.
var testAuthz = AuthorizerFunc(func(ctx context.Context) (string, bool, error) {
	org, _ := tenant.OrganizationFrom(ctx)
	owner, _ := ctx.Value(ownerKey{}).(bool)
	if org == "" && !owner {
		return "", false, errs.New(errs.CodeUnauthenticated, "No credentials.")
	}
	return org, owner, nil
})

type apiEnv struct {
	srv *httptest.Server
	sys *logging.System
	hub *store.Hub
}

func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	s := openStore(t, "n1")
	seedStore(t, s, 90, 0, time.Minute)
	svc, hub := localService(t, s)
	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	sys := logging.New(logging.Options{Node: "n1", Stdout: io.Discard, Fallback: io.Discard})
	t.Cleanup(func() { _ = sys.Close(context.Background()) })
	Register(srv, svc, APIOptions{System: sys, Authorizer: testAuthz, KeepAlive: 50 * time.Millisecond})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if org := r.Header.Get("X-Test-Org"); org != "" {
			ctx = tenant.WithOrganization(ctx, org)
		}
		if r.Header.Get("X-Test-Owner") == "1" {
			ctx = context.WithValue(ctx, ownerKey{}, true)
		}
		srv.ServeHTTP(w, r.WithContext(ctx))
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &apiEnv{srv: ts, sys: sys, hub: hub}
}

type caller struct{ org, owner string }

var (
	asA     = caller{org: orgA}
	asB     = caller{org: orgB}
	asOwner = caller{owner: "1"}
	nobody  = caller{}
)

// result is the status and headers of a finished request (the body is returned separately, already read
// and closed).
type result struct {
	StatusCode int
	Header     http.Header
}

func (e *apiEnv) do(t *testing.T, c caller, method, path string, body string, hdr map[string]string) (*result, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if c.org != "" {
		req.Header.Set("X-Test-Org", c.org)
	}
	if c.owner != "" {
		req.Header.Set("X-Test-Owner", c.owner)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	tr := &http.Transport{DisableCompression: true}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return &result{StatusCode: resp.StatusCode, Header: resp.Header}, b
}

func rangeQS(extra string) string {
	from, to := dayRange()
	v := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	return v.Encode() + extra
}

type listBody struct {
	Data       []store.LogRecord `json:"data"`
	NextCursor *string           `json:"nextCursor"`
	Partial    []string          `json:"partial"`
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func problemCode(t *testing.T, b []byte) string {
	t.Helper()
	return decode[map[string]any](t, b)["code"].(string)
}

func TestAPIListScopingAndPaging(t *testing.T) {
	e := newAPI(t)
	resp, b := e.do(t, asA, http.MethodGet, "/v1/logs?"+rangeQS("&limit=7&category=api&category=sync"), "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	total := 0
	for {
		lb := decode[listBody](t, b)
		for _, r := range lb.Data {
			if str(r.OrganizationID) != orgA {
				t.Fatalf("tenant leak: %+v", r)
			}
			total++
		}
		if lb.NextCursor == nil {
			break
		}
		resp, b = e.do(t, asA, http.MethodGet, "/v1/logs?"+rangeQS("&limit=7&cursor="+*lb.NextCursor), "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
	}
	if total != 30 {
		t.Fatalf("orgA saw %d records", total)
	}
	// owners see everything, including instance-level records, and may filter by organisation
	_, b = e.do(t, asOwner, http.MethodGet, "/v1/logs?"+rangeQS("&limit=500"), "", nil)
	if n := len(decode[listBody](t, b).Data); n != 90 {
		t.Fatalf("owner saw %d", n)
	}
	_, b = e.do(t, asOwner, http.MethodGet, "/v1/logs?"+rangeQS("&organizationId="+orgB), "", nil)
	for _, r := range decode[listBody](t, b).Data {
		if str(r.OrganizationID) != orgB {
			t.Fatal("owner org filter")
		}
	}
	// filters and the comma form of category
	_, b = e.do(t, asA, http.MethodGet, "/v1/logs?"+rangeQS("&category=api,sync&level=warn&q=connection"), "", nil)
	for _, r := range decode[listBody](t, b).Data {
		if r.Level != "warn" || !strings.Contains(r.Msg, "connection") {
			t.Fatalf("filters %+v", r)
		}
	}
	// empty result renders data [] and nextCursor null
	_, b = e.do(t, asA, http.MethodGet, "/v1/logs?"+rangeQS("&q="+url.QueryEscape(`" OR 1 --`)), "", nil)
	if !strings.Contains(string(b), `"data":[]`) || !strings.Contains(string(b), `"nextCursor":null`) {
		t.Fatalf("empty body %s", b)
	}
	for _, c := range []struct {
		who    caller
		qs     string
		status int
		code   string
	}{
		{asA, rangeQS("&organizationId=" + orgB), 403, "forbidden"},
		{nobody, rangeQS(""), 401, "unauthenticated"},
		{asA, "to=2026-10-20T00:00:00Z", 422, "validation_failed"},
		{asA, "from=2026-08-01T00:00:00Z&to=2026-10-20T00:00:00Z", 422, "log_range_too_large"},
		{asA, rangeQS("&limit=501"), 422, "validation_failed"},
		{asA, rangeQS("&category=bogus"), 422, "validation_failed"},
		{asA, rangeQS("&level=loud"), 422, "validation_failed"},
		{asA, rangeQS("&cursor=garbage!"), 422, "validation_failed"},
	} {
		resp, b := e.do(t, c.who, http.MethodGet, "/v1/logs?"+c.qs, "", nil)
		if resp.StatusCode != c.status || problemCode(t, b) != c.code {
			t.Errorf("%s: %d %s", c.qs, resp.StatusCode, b)
		}
	}
}

func TestAPIGet(t *testing.T) {
	e := newAPI(t)
	idA := newID(base.Add(-3*time.Minute), 3)    // i=3 → orgA
	idInst := newID(base.Add(-2*time.Minute), 2) // i=2 → instance level
	resp, b := e.do(t, asA, http.MethodGet, "/v1/logs/"+idA, "", nil)
	if resp.StatusCode != http.StatusOK || decode[store.LogRecord](t, b).ID != idA {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	for _, c := range []struct {
		who    caller
		id     string
		status int
	}{
		{asB, idA, 404}, {asA, idInst, 404}, {asOwner, idInst, 200}, {asA, newID(base, 77777), 404}, {asA, "log_bad", 422},
		{nobody, idA, 401},
	} {
		if resp, b := e.do(t, c.who, http.MethodGet, "/v1/logs/"+c.id, "", nil); resp.StatusCode != c.status {
			t.Errorf("%+v: %d %s", c, resp.StatusCode, b)
		}
	}
}

func TestAPIExport(t *testing.T) {
	e := newAPI(t)
	resp, b := e.do(t, asA, http.MethodGet, "/v1/logs/export?"+rangeQS(""), "", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("%d %v %s", resp.StatusCode, resp.Header, b)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 30 {
		t.Fatalf("export lines %d", len(lines))
	}
	for _, l := range lines {
		if str(decode[store.LogRecord](t, []byte(l)).OrganizationID) != orgA {
			t.Fatal("export tenant leak")
		}
	}
	resp, b = e.do(t, asOwner, http.MethodGet, "/v1/logs/export?"+rangeQS("&category=api"), "", map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip header %v", resp.Header)
	}
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		n++
	}
	if n != 45 {
		t.Fatalf("gzip lines %d", n)
	}
	if resp, b := e.do(t, asA, http.MethodGet, "/v1/logs/export?from=2026-01-01T00:00:00Z&to=2026-10-01T00:00:00Z", "", nil); resp.StatusCode != http.StatusUnprocessableEntity ||
		problemCode(t, b) != "log_range_too_large" {
		t.Fatalf("export range %d %s", resp.StatusCode, b)
	}
	if resp, _ := e.do(t, nobody, http.MethodGet, "/v1/logs/export?"+rangeQS(""), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("export auth")
	}
	for in, want := range map[string]bool{"gzip": true, "br, gzip;q=1": true, "gzip;q=0": false, "deflate": false, "": false} {
		if acceptsGzip(in) != want {
			t.Errorf("acceptsGzip(%q)", in)
		}
	}
}

func TestAPIStats(t *testing.T) {
	e := newAPI(t)
	_, b := e.do(t, asA, http.MethodGet, "/v1/logs/stats?"+rangeQS(""), "", nil)
	st := decode[LogStats](t, b)
	if st.ByCategory["api"]+st.ByCategory["sync"] != 30 || st.Disk != nil {
		t.Fatalf("org stats %s", b)
	}
	_, b = e.do(t, asOwner, http.MethodGet, "/v1/logs/stats?"+rangeQS(""), "", nil)
	st = decode[LogStats](t, b)
	if st.ByLevel["info"] != 45 || st.Disk["api"].Bytes <= 0 || st.Disk["api"].Nodes["n1"] != st.Disk["api"].Bytes {
		t.Fatalf("owner stats %s", b)
	}
	if resp, _ := e.do(t, asA, http.MethodGet, "/v1/logs/stats?from=2026-01-01T00:00:00Z&to=2026-10-01T00:00:00Z", "", nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatal("stats range")
	}
	if resp, _ := e.do(t, nobody, http.MethodGet, "/v1/logs/stats?"+rangeQS(""), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("stats auth")
	}
}

func TestAPISettings(t *testing.T) {
	e := newAPI(t)
	resp, b := e.do(t, asA, http.MethodGet, "/v1/logs/settings", "", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") == "" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	cur := decode[LogSettings](t, b)
	if *cur.Stdout != "off" || *cur.DiskCap != "5GiB" || *cur.Categories["sync"].Retention != "14d" || *cur.Categories["api"].Cap != "1GiB" {
		t.Fatalf("defaults %s", b)
	}
	mp := map[string]string{"Content-Type": "application/merge-patch+json"}
	patch := `{"stdout":"json","diskCap":"10GiB","hashAddresses":true,"categories":{"sync":{"level":"debug","retention":"7d","cap":"2GiB","sampling":{"debug":0.5,"trace":0.1}},"crash":{"enabled":false}}}`
	if resp, b := e.do(t, asA, http.MethodPatch, "/v1/logs/settings", patch, mp); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("org admin patch %d %s", resp.StatusCode, b)
	}
	if resp, _ := e.do(t, asOwner, http.MethodPatch, "/v1/logs/settings", patch, map[string]string{"Content-Type": "application/json", "If-Match": `"v0"`}); resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match %d", resp.StatusCode)
	}
	resp, b = e.do(t, asOwner, http.MethodPatch, "/v1/logs/settings", patch, map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": resp.Header.Get("ETag")})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", resp.StatusCode, b)
	}
	st := e.sys.Settings()
	sync := st.Categories[logging.CatSync]
	if st.Stdout != "json" || st.DiskCap != 10<<30 || !st.HashAddresses || sync.Level != logging.LevelDebug || sync.Retention != 7*24*time.Hour ||
		sync.Cap != 2<<30 || sync.Sampling[logging.LevelDebug] != 0.5 || st.Categories[logging.CatCrash].Enabled {
		t.Fatalf("applied %+v", st)
	}
	if st.Categories[logging.CatAPI].Retention != 14*24*time.Hour {
		t.Fatal("untouched category must keep its settings")
	}
	got := decode[LogSettings](t, b)
	if *got.Categories["sync"].Sampling.Debug != 0.5 || *got.Categories["sync"].Level != "debug" {
		t.Fatalf("response %s", b)
	}
	// sampling 1 removes the entry
	if resp, b := e.do(t, asOwner, http.MethodPatch, "/v1/logs/settings", `{"categories":{"sync":{"sampling":{"debug":1,"trace":1}}}}`, mp); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if e.sys.Settings().Categories[logging.CatSync].Sampling != nil {
		t.Fatal("sampling 1 must clear")
	}
	for _, bad := range []string{
		`{"categories":{"nope":{"level":"info"}}}`,
		`{"categories":{"sync":{"retention":"forever"}}}`,
		`{"categories":{"sync":{"retention":"1m"}}}`,
		`{"categories":{"sync":{"cap":"12"}}}`,
		`{"categories":{"sync":{"level":"loud"}}}`,
		`{"categories":{"sync":{"sampling":{"debug":2}}}}`,
		`{"diskCap":"lots"}`,
		`{"stdout":"syslog"}`,
		`{"unknownField":true}`,
	} {
		if resp, b := e.do(t, asOwner, http.MethodPatch, "/v1/logs/settings", bad, mp); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", bad, resp.StatusCode, b)
		}
	}
	if resp, _ := e.do(t, nobody, http.MethodGet, "/v1/logs/settings", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("settings auth")
	}
	if resp, _ := e.do(t, nobody, http.MethodPatch, "/v1/logs/settings", `{}`, mp); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("patch auth")
	}
}

func TestAPIStreamSSE(t *testing.T) {
	e := newAPI(t)
	if resp, b := e.do(t, asA, http.MethodGet, "/v1/logs/stream?level=loud", "", nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("stream validation %d %s", resp.StatusCode, b)
	}
	if resp, _ := e.do(t, nobody, http.MethodGet, "/v1/logs/stream", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("stream auth")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/v1/logs/stream?category=api", nil)
	req.Header.Set("X-Test-Org", orgA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream %d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	want := mkRec(time.Now(), 5, logging.CatAPI, logging.LevelInfo, "streamed", orgA)
	// wait until the subscription exists, then publish
	deadline := time.Now().Add(3 * time.Second)
	for e.hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_ = e.hub.Write(ctx, []logging.Record{mkRec(time.Now(), 4, logging.CatAPI, logging.LevelInfo, "other tenant", orgB), want})
	var gotID, gotData bool
	keepalive := false
	timeout := time.After(3 * time.Second)
	for !gotID || !gotData || !keepalive {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream closed")
			}
			switch {
			case l == "id: "+want.ID:
				gotID = true
			case strings.HasPrefix(l, "data: "):
				r := decode[store.LogRecord](t, []byte(strings.TrimPrefix(l, "data: ")))
				if r.Msg != "streamed" {
					t.Fatalf("stream leaked %q", r.Msg)
				}
				gotData = true
			case l == ":keepalive":
				keepalive = true
			}
		case <-timeout:
			t.Fatalf("id=%v data=%v keepalive=%v", gotID, gotData, keepalive)
		}
	}
	cancel()
	deadline = time.Now().Add(3 * time.Second)
	for e.hub.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("closing the stream must end the subscription")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenAPIDocumentListsOperations(t *testing.T) {
	e := newAPI(t)
	_, b := e.do(t, asA, http.MethodGet, "/v1/openapi.json", "", nil)
	doc := string(b)
	for _, s := range []string{`"listLogs"`, `"getLog"`, `"exportLogs"`, `"getLogStats"`, `"getLogSettings"`, `"patchLogSettings"`,
		`"streamLogs"`, `"LogRecord"`, `"LogRecordList"`, `"LogStats"`, `"LogSettings"`, `"text/event-stream"`, `"application/x-ndjson"`} {
		if !strings.Contains(doc, s) {
			t.Errorf("OpenAPI document lacks %s", s)
		}
	}
}

func TestModuleRegistersAPI(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	sys := logging.New(logging.Options{Node: "n1", Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(ctx) }()
	a := app.New(app.Options{NodeID: "n1"})
	m := &Module{System: sys}
	if m.Name() != "logging.query" || len(m.Roles()) != 1 {
		t.Fatal("identity")
	}
	if err := m.Start(ctx, a); err == nil {
		t.Fatal("start without server")
	}
	app.Provide(a, srv)
	if err := m.Start(ctx, a); err == nil {
		t.Fatal("start without bus or node")
	}
	s := openStore(t, "n1")
	app.Provide(a, &store.Node{Store: s})
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Get[*Service](a); !ok {
		t.Fatal("service not provided")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logs/settings", nil))
	if rec.Code != http.StatusForbidden { // default TenantAuthorizer without an organisation
		t.Fatalf("default authorizer %d %s", rec.Code, rec.Body)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (&Module{}).Start(ctx, a); err == nil {
		t.Fatal("module without system")
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
