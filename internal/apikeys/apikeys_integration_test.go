//go:build integration

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testStore(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebapikeys_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB replica set at %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

type harness struct {
	t          *testing.T
	db         *store.DB
	bus        *bus.Bus
	svc        *Service
	org        string
	authUserID string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testStore(t)
	b, err := bus.Open(context.Background(), bus.Options{
		Mode: bus.ModeEmbedded, NodeID: "t", DataDir: t.TempDir(), Log: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	authSvc, err := auth.New(ctx, auth.Options{
		Auth: config.AuthConfig{
			Secret: "0123456789abcdef0123456789abcdef", CookieSecure: false,
			SessionTTL: 12 * time.Hour, SessionIdle: 2 * time.Hour,
			MFARequired: false, WebAuthnRPID: "localhost",
			LockoutThreshold: 50, LockoutDuration: time.Minute,
		},
		PublicURL: "http://localhost",
		Env:       "development",
		LoginRate: config.Rate{Count: 1000, Period: time.Minute},
		Log:       quiet(),
	}, db.DB, b)
	if err != nil {
		t.Fatal(err)
	}
	aud := &audit.Writer{DB: db, Log: quiet()}
	svc := New(db, authSvc, b, aud, quiet())
	authID, err := authSvc.CreateUser(ctx, "owner@example.test", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		t: t, db: db, bus: b, svc: svc,
		org: store.NewID(store.PrefixOrganization), authUserID: authID,
	}
}

func (h *harness) asOwner() context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		AuthUserID: h.authUserID, OrganizationID: h.org, Role: tenancy.RoleOwner,
	})
}

func (h *harness) asKey(scopes []string, keyID string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: keyID, OrganizationID: h.org, Scopes: scopes,
	})
}

func (h *harness) resolveThroughHTTP(token, forwardedFor string, referer string) (*tenancy.Caller, error) {
	srv, err := httpapi.New(httpapi.Options{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		AccessLog:      quiet(), SecurityLog: quiet(),
	})
	if err != nil {
		return nil, err
	}
	var got *tenancy.Caller
	var gotErr error
	srv.Router.Get("/check", func(w http.ResponseWriter, r *http.Request) {
		got, gotErr = h.svc.Resolve(r)
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return got, gotErr
}

func TestIntegrationCreateUseScopeIPPatchRevokeExpire(t *testing.T) {
	h := newHarness(t)
	ctx := h.asOwner()

	// create
	k, token, err := h.svc.Create(ctx, CreateInput{
		Name: "main", Scopes: []string{"accounts:read", "logs:read"},
		Restrictions: &Restrictions{IPs: []string{"203.0.113.0/24"}, Referrers: []string{"https://ok.example/*"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || !strings.HasPrefix(token, auth.APIKeyPrefix) || k.Last4 == "" || k.LimenKeyHash == "" {
		t.Fatalf("token/meta: %q %+v", token, k)
	}

	// use — allowed IP + referrer
	c, err := h.resolveThroughHTTP(token, "203.0.113.50", "https://ok.example/app")
	if err != nil || c == nil || c.APIKeyID != k.ID || c.OrganizationID != h.org {
		t.Fatalf("resolve: %+v %v", c, err)
	}
	if len(c.Scopes) != 2 {
		t.Fatal(c.Scopes)
	}

	// scope denied at Require (key lacks settings:write)
	ctxKey := tenancy.WithCaller(context.Background(), *c)
	if err := tenancy.Require(ctxKey, tenancy.PermSettingsWrite); err == nil || errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("want scope_missing, got %v", err)
	}

	// IP denied
	_, err = h.resolveThroughHTTP(token, "198.51.100.1", "https://ok.example/app")
	if errs.CodeOf(err) != errs.CodeAPIKeyIPDenied {
		t.Fatalf("ip: %v", err)
	}

	// referrer denied
	_, err = h.resolveThroughHTTP(token, "203.0.113.50", "https://evil.test/")
	if errs.CodeOf(err) != errs.CodeAPIKeyReferrerDenied {
		t.Fatalf("referrer: %v", err)
	}

	// patch name + scopes
	newScopes := []string{"accounts:write", "logs:read"}
	name := "renamed"
	upd, err := h.svc.Patch(ctx, k.ID, PatchInput{Name: &name, Scopes: &newScopes}, formatETag(k.Version))
	if err != nil || upd.Name != "renamed" || upd.Version != 2 {
		t.Fatalf("patch: %+v %v", upd, err)
	}

	// list
	list, err := h.svc.List(ctx, ListFilter{Page: 1, Limit: 10, IncludeTotal: true, Sort: "name"})
	if err != nil || len(list.Data) != 1 || list.Total == nil || *list.Total != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// get
	got, err := h.svc.Get(ctx, k.ID)
	if err != nil || got.Name != "renamed" {
		t.Fatal(got, err)
	}

	// API key creating a child key — may only grant held scopes
	childCtx := h.asKey([]string{"accounts:write", "apikeys:write"}, k.ID)
	_, _, err = h.svc.Create(childCtx, CreateInput{Name: "child", Scopes: []string{"settings:write"}})
	if errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("child over-scope: %v", err)
	}
	child, childTok, err := h.svc.Create(childCtx, CreateInput{Name: "child", Scopes: []string{"accounts:read"}})
	if err != nil || childTok == "" {
		t.Fatal(err)
	}
	_ = child

	// revoke — immediate (cache purged)
	if err := h.svc.Revoke(ctx, k.ID, formatETag(upd.Version)); err != nil {
		t.Fatal(err)
	}
	_, err = h.resolveThroughHTTP(token, "203.0.113.50", "https://ok.example/app")
	code := errs.CodeOf(err)
	if code != errs.CodeAPIKeyInvalid && code != errs.CodeAPIKeyRevoked {
		t.Fatalf("after revoke: %v", err)
	}
	// idempotent revoke
	if err := h.svc.Revoke(ctx, k.ID, ""); err != nil {
		t.Fatal(err)
	}

	// expired key
	exp := time.Now().UTC().Add(2 * time.Second)
	k2, tok2, err := h.svc.Create(ctx, CreateInput{
		Name: "temp", Scopes: []string{"logs:read"}, ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.resolveThroughHTTP(tok2, "", ""); err != nil {
		t.Fatal(err)
	}
	// force expiry in our metadata + Limen
	past := time.Now().UTC().Add(-time.Minute)
	_, _ = h.db.Collection(Collection).UpdateOne(ctx, bson.M{"_id": k2.ID}, bson.M{"$set": bson.M{"expiresAt": past}})
	_, _ = h.db.Collection(limenAPIKeys).UpdateOne(ctx, bson.M{"_id": k2.LimenKeyID}, bson.M{"$set": bson.M{"expires_at": past}})
	h.svc.purgeVerifiedCache(ctx, k2.LimenKeyHash)
	_, err = h.resolveThroughHTTP(tok2, "", "")
	code = errs.CodeOf(err)
	if code != errs.CodeAPIKeyExpired && code != errs.CodeAPIKeyInvalid {
		t.Fatalf("expired: %v", err)
	}
}

func TestIntegrationModuleAndHTTP(t *testing.T) {
	h := newHarness(t)
	quietLog := quiet()
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quietLog})
	app.Provide(a, h.db)
	app.Provide(a, h.bus)
	authSvc := h.svc.Auth
	app.Provide(a, authSvc)
	aud := &audit.Writer{DB: h.db, Log: quietLog}
	app.Provide(a, aud)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quietLog, SecurityLog: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Log: quietLog}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "apikeys" || m.Service() == nil || m.Stop(context.Background()) != nil {
		t.Fatal("module identity")
	}
	if _, ok := app.Get[*Service](a); !ok {
		t.Fatal("service not provided")
	}
	if _, ok := app.Get[tenancy.KeyResolver](a); !ok {
		t.Fatal("resolver not provided")
	}
}

func TestIntegrationInvalidBearer(t *testing.T) {
	h := newHarness(t)
	c, err := h.resolveThroughHTTP("", "", "")
	if c != nil || err != nil {
		t.Fatal(c, err)
	}
	_, err = h.resolveThroughHTTP("not-a-key", "", "")
	if errs.CodeOf(err) != errs.CodeAPIKeyInvalid {
		t.Fatalf("%v", err)
	}
	_, err = h.resolveThroughHTTP(auth.APIKeyPrefix+"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "", "")
	if errs.CodeOf(err) != errs.CodeAPIKeyInvalid {
		t.Fatalf("%v", err)
	}
	_, err = h.resolveThroughHTTP("Bearer", "", "") // empty after trim via Resolve path with "Bearer " only handled by HasPrefix
	_ = err
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer ")
	if _, err := h.svc.Resolve(r); errs.CodeOf(err) != errs.CodeAPIKeyInvalid {
		t.Fatalf("empty bearer: %v", err)
	}
}

func TestIntegrationHTTPHandlersAndPatchList(t *testing.T) {
	h := newHarness(t)
	quietLog := quiet()
	srv, err := httpapi.New(httpapi.Options{AccessLog: quietLog, SecurityLog: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{Log: quietLog, svc: h.svc}
	m.registerAPI(srv)

	ctx := h.asOwner()
	// seed via service then hit HTTP with caller middleware
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handler := mw(srv)

	call := func(method, path, body string, hdr map[string]string) (int, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	code, body := call(http.MethodPost, "/v1/api-keys", `{"name":"http","scopes":["logs:read","apikeys:write"]}`, nil)
	if code != http.StatusCreated || !strings.Contains(body, `"token"`) {
		t.Fatalf("create %d %s", code, body)
	}
	// extract id
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatal(body, err)
	}

	code, body = call(http.MethodGet, "/v1/api-keys?page=1&limit=10&includeTotal=true&sort=name", "", nil)
	if code != 200 || !strings.Contains(body, created.ID) {
		t.Fatalf("list %d %s", code, body)
	}
	code, body = call(http.MethodGet, "/v1/api-keys/"+created.ID, "", nil)
	if code != 200 || !strings.Contains(body, `"http"`) {
		t.Fatalf("get %d %s", code, body)
	}
	accID := store.NewID(store.PrefixAccount)
	if _, err := h.db.Collection("accounts").InsertOne(ctx, bson.M{"_id": accID, "organizationId": h.org}); err != nil {
		t.Fatal(err)
	}
	code, body = call(http.MethodPatch, "/v1/api-keys/"+created.ID,
		`{"name":"http2","accountId":"`+accID+`","rateLimit":{"rps":1,"burst":2},"restrictions":{"ips":["127.0.0.1/32"]}}`,
		map[string]string{"If-Match": `"v1"`})
	if code != 200 || !strings.Contains(body, `"http2"`) {
		t.Fatalf("patch %d %s", code, body)
	}
	// Missing accountId → 404; unknown shape still 422.
	code, body = call(http.MethodPatch, "/v1/api-keys/"+created.ID,
		`{"accountId":"acc_01h00000000000000000000000"}`,
		map[string]string{"If-Match": `"v2"`})
	if code != http.StatusNotFound {
		t.Fatalf("missing accountId: %d %s", code, body)
	}
	code, body = call(http.MethodPatch, "/v1/api-keys/"+created.ID,
		`{"accountId":null,"expiresAt":null}`,
		map[string]string{"If-Match": `"v2"`})
	if code != 200 {
		t.Fatalf("patch null %d %s", code, body)
	}
	exp := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	code, body = call(http.MethodPatch, "/v1/api-keys/"+created.ID,
		`{"expiresAt":"`+exp+`"}`,
		map[string]string{"If-Match": `"v3"`})
	if code != 200 {
		t.Fatalf("patch exp %d %s", code, body)
	}
	code, _ = call(http.MethodDelete, "/v1/api-keys/"+created.ID, "", map[string]string{"If-Match": `"v4"`})
	if code != http.StatusNoContent && code != 200 {
		t.Fatalf("delete %d", code)
	}

	// service-level edges: validation, get missing, list cursor, version mismatch
	_, _, err = h.svc.Create(ctx, CreateInput{Name: "", Scopes: []string{"logs:read"}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	_, _, err = h.svc.Create(ctx, CreateInput{Name: "x", Scopes: []string{"logs:read"}, ExpiresAt: &past})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := h.svc.Get(ctx, "key_01h00000000000000000000000"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := h.svc.Get(ctx, "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	k1, _, _ := h.svc.Create(ctx, CreateInput{Name: "a", Scopes: []string{"logs:read"}})
	k2, _, _ := h.svc.Create(ctx, CreateInput{Name: "b", Scopes: []string{"logs:read"}})
	list, err := h.svc.List(ctx, ListFilter{Limit: 1, Sort: "-id"})
	if err != nil || list.NextCursor == nil {
		t.Fatal(list, err)
	}
	list2, err := h.svc.List(ctx, ListFilter{Limit: 10, Cursor: *list.NextCursor, Sort: "-id"})
	if err != nil || len(list2.Data) < 1 {
		t.Fatal(list2, err)
	}
	_, err = h.svc.Patch(ctx, k1.ID, PatchInput{}, `"v99"`)
	if errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	accDocID := store.NewID(store.PrefixAccount)
	if _, err := h.db.Collection("accounts").InsertOne(ctx, bson.M{"_id": accDocID, "organizationId": h.org}); err != nil {
		t.Fatal(err)
	}
	acc := &nullableString{Present: true, Value: accDocID}
	rl := &RateLimit{RPS: 5, Burst: 10}
	_, err = h.svc.Patch(ctx, k2.ID, PatchInput{AccountID: acc, RateLimit: rl}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Patch(ctx, k2.ID, PatchInput{AccountID: &nullableString{Present: true, Value: "acc_01h00000000000000000000000"}}, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing accountId: %v", err)
	}
	clearAcc := &nullableString{Present: true, Null: true}
	clearExp := &nullableTime{Present: true, Null: true}
	_, err = h.svc.Patch(ctx, k2.ID, PatchInput{AccountID: clearAcc, ExpiresAt: clearExp}, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = k1
}
