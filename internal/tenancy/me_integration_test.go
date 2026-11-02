//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

type captureAuditor struct {
	actions []string
}

func (a *captureAuditor) Record(_ context.Context, action, _, _ string, _ map[string]FieldDiff) error {
	a.actions = append(a.actions, action)
	return nil
}

func TestIntegrationPatchMe(t *testing.T) {
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
	aud := &captureAuditor{}
	app.Provide(a, Auditor(aud))

	owner := &User{AuthUserID: "auth-patch", Email: "patch@example.test", Name: "Old", Locale: "it", InstanceRole: "owner"}
	repo := &Repo{DB: db}
	if err := repo.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	org, err := repo.CreateOrganization(ctx, "PatchOrg", "patch-org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, org.ID, owner.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}

	ident := func(ctx context.Context) (Identity, bool) {
		v, ok := ctx.Value(idKey{}).(Identity)
		return v, ok
	}
	keys := func(r *http.Request) (*Caller, error) {
		if r.Header.Get("Authorization") == "Bearer ebk_good" {
			return &Caller{Kind: CallerAPIKey, APIKeyID: "key_1", OrganizationID: org.ID, Scopes: []string{"admin"}}, nil
		}
		return nil, nil
	}
	m := &Module{Identity: ident, Keys: keys}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}

	call := func(method, body string, withUser, withKey bool) (int, map[string]any) {
		r := httptest.NewRequest(method, "/v1/me", strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if withKey {
			r.Header.Set("Authorization", "Bearer ebk_good")
		}
		if withUser {
			r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: owner.AuthUserID, Email: owner.Email, MFA: true}))
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, body := call(http.MethodPatch, `{"name":"New Name","locale":"en"}`, true, false)
	if code != 200 {
		t.Fatalf("patch: %d %v", code, body)
	}
	user, _ := body["user"].(map[string]any)
	if user["name"] != "New Name" || user["locale"] != "en" {
		t.Fatalf("updated user: %v", user)
	}
	if len(aud.actions) != 1 || aud.actions[0] != "user.update" {
		t.Fatalf("audit: %v", aud.actions)
	}
	// no-op patch still 200
	code, _ = call(http.MethodPatch, `{"name":"New Name","locale":"en"}`, true, false)
	if code != 200 {
		t.Fatalf("noop: %d", code)
	}
	code, body = call(http.MethodPatch, `{"name":""}`, true, false)
	if code != 422 || body["code"] != string(errs.CodeValidationFailed) {
		t.Fatalf("empty name: %d %v", code, body)
	}
	code, body = call(http.MethodPatch, `{"locale":"xx"}`, true, false)
	if code != 422 || body["code"] != string(errs.CodeValidationFailed) {
		t.Fatalf("bad locale: %d %v", code, body)
	}
	code, body = call(http.MethodPatch, `{"name":"X"}`, false, true)
	if code != 403 || body["code"] != string(errs.CodeForbidden) {
		t.Fatalf("api key patch: %d %v", code, body)
	}
	code, body = call(http.MethodPatch, `{"name":"X"}`, false, false)
	if code != 401 {
		t.Fatalf("anon patch: %d %v", code, body)
	}
}
