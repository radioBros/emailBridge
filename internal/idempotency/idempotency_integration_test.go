//go:build integration

package idempotency

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fixture struct {
	t     *testing.T
	db    *store.DB
	mw    *Middleware
	calls atomic.Int64
	gate  chan struct{} // when set, the handler blocks on it
	reply func(w http.ResponseWriter, r *http.Request)
	srv   *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_idem_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	f := &fixture{t: t, db: db}
	f.mw = &Middleware{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	f.reply = func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Load()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", fmt.Sprintf("/v1/things/t%d", n))
		w.Header().Set("Set-Cookie", "never=stored")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":"t%d"}`, n)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		_, _ = io.ReadAll(r.Body)
		if f.gate != nil {
			<-f.gate
		}
		f.reply(w, r)
	})
	// Platform order: body limit → caller resolution (header X-Test-Org, absent = anonymous) → idempotency.
	withCaller := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			if org := r.Header.Get("X-Test-Org"); org != "" {
				r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{
					Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
				}))
			}
			defer func() {
				if v := recover(); v != nil {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
	f.srv = httptest.NewServer(withCaller(f.mw.Handler(handler)))
	t.Cleanup(f.srv.Close)
	return f
}

type resp struct {
	status int
	header http.Header
	body   string
}

func (f *fixture) do(method, path, org, key, body string) resp {
	f.t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body)) //nolint:noctx // test
	if err != nil {
		f.t.Fatal(err)
	}
	if org != "" {
		req.Header.Set("X-Test-Org", org)
	}
	if key != "" {
		req.Header.Set(Header, key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: string(b)}
}

const orgA, orgB = "org_a", "org_b"

func TestIntegrationReplaySameRequest(t *testing.T) {
	f := newFixture(t)
	first := f.do(http.MethodPost, "/v1/things", orgA, "k1", `{"a":1,"b":2}`)
	if first.status != http.StatusCreated || first.header.Get(ReplayedHeader) != "" {
		t.Fatalf("first: %+v", first)
	}
	// Same JSON with another key order and spacing replays.
	again := f.do(http.MethodPost, "/v1/things", orgA, "k1", `{ "b": 2, "a": 1 }`)
	if again.status != http.StatusCreated || again.body != first.body || again.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("replay: %+v", again)
	}
	if again.header.Get("Location") != first.header.Get("Location") || again.header.Get("Set-Cookie") != "" {
		t.Fatalf("replay headers: %v", again.header)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", f.calls.Load())
	}
}

func TestIntegrationReusedKeyDifferentRequest(t *testing.T) {
	f := newFixture(t)
	_ = f.do(http.MethodPost, "/v1/things", orgA, "k1", `{"a":1}`)
	r := f.do(http.MethodPost, "/v1/things", orgA, "k1", `{"a":2}`)
	if r.status != http.StatusConflict || !strings.Contains(r.body, "idempotency_key_reused") {
		t.Fatalf("%+v", r)
	}
	q := f.do(http.MethodPost, "/v1/things?x=1", orgA, "k1", `{"a":1}`) // the query is part of the request
	if q.status != http.StatusConflict {
		t.Fatalf("query variant: %+v", q)
	}
}

func TestIntegrationScopes(t *testing.T) {
	f := newFixture(t)
	_ = f.do(http.MethodPost, "/v1/things", orgA, "k1", `{}`)
	if r := f.do(http.MethodPost, "/v1/things", orgB, "k1", `{}`); r.status != http.StatusCreated || r.header.Get(ReplayedHeader) != "" {
		t.Fatalf("other organisation must not replay: %+v", r)
	}
	if r := f.do(http.MethodPost, "/v1/other", orgA, "k1", `{}`); r.header.Get(ReplayedHeader) != "" {
		t.Fatalf("other route must not replay: %+v", r)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("calls=%d", f.calls.Load())
	}
}

func TestIntegrationInProgressThenStale(t *testing.T) {
	f := newFixture(t)
	f.gate = make(chan struct{})
	var wg sync.WaitGroup
	var first resp
	wg.Add(1)
	go func() { defer wg.Done(); first = f.do(http.MethodPost, "/v1/things", orgA, "k1", `{}`) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r := f.do(http.MethodPost, "/v1/things", orgA, "k1", `{}`); r.status != http.StatusConflict || !strings.Contains(r.body, "idempotency_in_progress") {
		t.Fatalf("concurrent duplicate: %+v", r)
	}
	close(f.gate)
	wg.Wait()
	if first.status != http.StatusCreated {
		t.Fatalf("first: %+v", first)
	}

	// A record left running by a dead node is taken over once it is stale.
	f.gate = nil
	id := ID(orgA, http.MethodPost, "/v1/stale", "k2")
	old := time.Now().UTC().Add(-time.Hour)
	if _, err := f.db.Collection(Collection).InsertOne(context.Background(), record{
		ID: id, OrganizationID: orgA, Method: http.MethodPost, Route: "/v1/stale", RequestHash: Hash("", []byte(`{}`)),
		State: stateRunning, CreatedAt: old, ExpireAt: old.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if r := f.do(http.MethodPost, "/v1/stale", orgA, "k2", `{}`); r.status != http.StatusCreated {
		t.Fatalf("stale takeover: %+v", r)
	}
}

func TestIntegrationFailuresAreNotStored(t *testing.T) {
	f := newFixture(t)
	f.reply = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnprocessableEntity) }
	_ = f.do(http.MethodPost, "/v1/things", orgA, "k1", `{}`)
	_ = f.do(http.MethodPost, "/v1/things", orgA, "k1", `{}`)
	if f.calls.Load() != 2 {
		t.Fatalf("a failed request must run again: calls=%d", f.calls.Load())
	}

	f.reply = func(http.ResponseWriter, *http.Request) { panic("boom") }
	_ = f.do(http.MethodPost, "/v1/panics", orgA, "k3", `{}`)
	n, err := f.db.Collection(Collection).CountDocuments(context.Background(), bson.M{"route": "/v1/panics"})
	if err != nil || n != 0 {
		t.Fatalf("a panicking request must release its key: %d %v", n, err)
	}

	big := strings.Repeat("x", maxStored+1)
	f.reply = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, big) }
	before := f.calls.Load()
	_ = f.do(http.MethodPost, "/v1/big", orgA, "k4", `{}`)
	if r := f.do(http.MethodPost, "/v1/big", orgA, "k4", `{}`); len(r.body) != len(big) || f.calls.Load()-before != 2 {
		t.Fatalf("an oversized response is not stored: calls=%d", f.calls.Load()-before)
	}
}

func TestIntegrationPassThroughAndValidation(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ method, path, org, key string }{
		{http.MethodPost, "/v1/things", orgA, ""},                             // no key
		{http.MethodGet, "/v1/things", orgA, "k1"},                            // not a POST
		{http.MethodPost, "/v1/accounts/acc_1/outbound-messages", orgA, "k1"}, // own implementation
		{http.MethodPost, "/v1/auth/sign-in/email", orgA, "k1"},               // never replay a sign-in
		{http.MethodPost, "/v1/things", "", "k1"},                             // anonymous
	} {
		before := f.calls.Load()
		_ = f.do(tc.method, tc.path, tc.org, tc.key, `{}`)
		_ = f.do(tc.method, tc.path, tc.org, tc.key, `{}`)
		if f.calls.Load()-before != 2 {
			t.Fatalf("%s %s must pass through", tc.method, tc.path)
		}
	}
	if r := f.do(http.MethodPost, "/v1/things", orgA, strings.Repeat("k", 256), `{}`); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("long key: %+v", r)
	}
	if r := f.do(http.MethodPost, "/v1/things", orgA, "kä", `{}`); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("non-ASCII key: %+v", r)
	}
	if r := f.do(http.MethodPost, "/v1/things", orgA, "k9", strings.Repeat("y", 2048)); r.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("body over the limit: %+v", r)
	}
}

// One-time secrets (API key tokens, webhook secrets) are never stored, so a replay never shows them again.
func TestIntegrationSecretsNeverStored(t *testing.T) {
	f := newFixture(t)
	f.reply = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"key_1","token":"eb_live_TOPSECRET","secretPreview":"whsec_…AB","hasPassword":true,`+
			`"nested":[{"clientSecret":"nested-secret","name":"x"}]}`)
	}
	first := f.do(http.MethodPost, "/v1/api-keys", orgA, "k-secret", `{}`)
	if !strings.Contains(first.body, "eb_live_TOPSECRET") {
		t.Fatalf("the first response carries the token: %s", first.body)
	}
	var stored bson.M
	if err := f.db.Collection(Collection).FindOne(context.Background(), bson.M{"route": "/v1/api-keys"}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprint(stored)
	if strings.Contains(raw, "TOPSECRET") || strings.Contains(raw, "nested-secret") {
		t.Fatalf("a secret was stored: %s", raw)
	}
	again := f.do(http.MethodPost, "/v1/api-keys", orgA, "k-secret", `{}`)
	if again.header.Get(ReplayedHeader) != "true" || strings.Contains(again.body, "TOPSECRET") ||
		!strings.Contains(again.body, `"token":"[redacted]"`) || !strings.Contains(again.body, `"secretPreview":"whsec_…AB"`) ||
		!strings.Contains(again.body, `"hasPassword":true`) || !strings.Contains(again.body, `"name":"x"`) {
		t.Fatalf("replay: %s", again.body)
	}

	f.reply = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "plain text, maybe a secret") }
	before := f.calls.Load()
	_ = f.do(http.MethodPost, "/v1/text", orgA, "k-text", `{}`)
	_ = f.do(http.MethodPost, "/v1/text", orgA, "k-text", `{}`)
	if f.calls.Load()-before != 2 {
		t.Fatal("a non-JSON body is not stored, so the retry runs again")
	}
}
