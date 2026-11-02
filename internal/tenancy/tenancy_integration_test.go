//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_ten_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

type idKey struct{}

func TestIntegrationBootstrapResolverAndMe(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	created := 0
	creator := func(_ context.Context, email, _ string) (string, error) { created++; return "auth-" + email, nil }
	ident := func(ctx context.Context) (Identity, bool) {
		v, ok := ctx.Value(idKey{}).(Identity)
		return v, ok
	}
	keys := func(r *http.Request) (*Caller, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer ebk_good":
			return &Caller{Kind: CallerAPIKey, APIKeyID: "key_1", OrganizationID: "org_k", Scopes: []string{"logs:read"}}, nil
		case "Bearer ebk_bad":
			return nil, errs.New(errs.CodeAPIKeyInvalid, "bad key")
		}
		return nil, nil
	}
	m := &Module{Identity: ident, Keys: keys, Creator: creator}
	m.Bootstrap.Email, m.Bootstrap.Password, m.Bootstrap.OrgName = "owner@example.test", "pw-123456789012", "Acme"
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "tenancy" || len(m.Roles()) != 1 || m.Stop(ctx) != nil {
		t.Fatal("identity")
	}
	// bootstrap is idempotent
	if again, err := m.service.Bootstrap(ctx, creator, "x@y.z", "p", "Other"); err != nil || again || created != 1 {
		t.Fatalf("bootstrap twice: %v %v %d", again, err, created)
	}
	if ok, _ := m.service.Bootstrap(ctx, creator, "", "", ""); ok {
		t.Fatal("empty bootstrap must be a no-op")
	}
	owner, err := m.service.Repo.UserByAuthID(ctx, "auth-owner@example.test")
	if err != nil || owner.InstanceRole != "owner" {
		t.Fatalf("owner profile: %+v %v", owner, err)
	}
	// a second organisation the owner is also a member of (as viewer)
	org2, _ := m.service.Repo.CreateOrganization(ctx, "Beta", "beta")
	if _, err := m.service.Repo.CreateMembership(ctx, org2.ID, owner.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := m.service.Repo.CreateMembership(ctx, org2.ID, owner.ID, RoleAdmin); err == nil {
		t.Fatal("duplicate membership must be rejected")
	}
	call := func(hdr map[string]string, withIdentity bool) (int, map[string]any) {
		r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if withIdentity {
			r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: owner.AuthUserID, Email: owner.Email, MFA: true}))
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	code, body := call(nil, true)
	if code != 200 || body["role"] != "owner" || body["kind"] != "user" || body["mfa"] != true || len(body["memberships"].([]any)) != 2 {
		t.Fatalf("me (default org): %d %v", code, body)
	}
	if perms := fmt.Sprint(body["permissions"]); !strings.Contains(perms, "instance.admin") {
		t.Fatalf("instance owner permission missing: %s", perms)
	}
	code, body = call(map[string]string{"EB-Organization": org2.ID}, true)
	if code != 200 || body["role"] != "viewer" || body["activeOrganizationId"] != org2.ID {
		t.Fatalf("me (org2): %d %v", code, body)
	}
	// last used organisation is remembered
	_, body = call(nil, true)
	if body["activeOrganizationId"] != org2.ID {
		t.Fatalf("last organisation not remembered: %v", body)
	}
	// instance owner may act in an organisation without membership (viewer rights)
	org3, _ := m.service.Repo.CreateOrganization(ctx, "Gamma", "gamma")
	code, body = call(map[string]string{"EB-Organization": org3.ID}, true)
	if code != 200 || body["role"] != "viewer" {
		t.Fatalf("instance owner elsewhere: %d %v", code, body)
	}
	// unauthenticated → 401 problem from Require
	if code, body = call(nil, false); code != 401 || body["code"] != "unauthenticated" {
		t.Fatalf("anonymous: %d %v", code, body)
	}
	// API keys
	if code, body = call(map[string]string{"Authorization": "Bearer ebk_good"}, false); code != 200 || body["kind"] != "apiKey" || len(body["permissions"].([]any)) != 1 { // logs:read only: no organisation-wide read
		t.Fatalf("key me: %d %v", code, body)
	}
	if code, body = call(map[string]string{"Authorization": "Bearer ebk_good", "EB-Organization": "org_other"}, false); code != 403 || body["code"] != "organization_mismatch" {
		t.Fatalf("key org mismatch: %d %v", code, body)
	}
	if code, body = call(map[string]string{"Authorization": "Bearer ebk_bad"}, false); code != 401 {
		t.Fatalf("bad key: %d %v", code, body)
	}
	// identity without a profile; disabled user
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: "ghost"}))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("ghost: %d", w.Code)
	}
	if _, err := db.Collection(UsersCollection).UpdateOne(ctx, map[string]any{"_id": owner.ID}, map[string]any{"$set": map[string]any{"status": "disabled"}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(nil, true); code != 403 {
		t.Fatalf("disabled user: %d", code)
	}
	if _, err := m.service.Repo.Organization(ctx, "org_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing org")
	}
	if o, err := m.service.Repo.Organization(ctx, org2.ID); err != nil || o.Slug != "beta" {
		t.Fatal("org get")
	}
	// non-member, non-owner asking for an org
	plain := &User{AuthUserID: "auth-plain", Email: "Plain@Example.test"}
	if err := m.service.Repo.CreateUser(ctx, plain); err != nil || plain.Email != "plain@example.test" {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	r.Header.Set("EB-Organization", org2.ID)
	r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: "auth-plain"}))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("non-member: %d %s", w.Code, w.Body)
	}
}
