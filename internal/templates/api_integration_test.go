//go:build integration

package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationAPIModuleListCursorFeatureOff(t *testing.T) {
	db := testDB(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Registry: features.DefaultRegistry(), Env: "production", Log: quiet}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil || m.Service().Repo == nil {
		t.Fatal("service not wired")
	}
	org := store.NewID(store.PrefixOrganization)
	caller := tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleOwner,
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r = r.WithContext(tenancy.WithCaller(r.Context(), caller))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	w := do(http.MethodPost, "/v1/templates", `{"name":"one","text":"Hello {{name}}","html":"<b>{{name}}</b>","strict":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created Template
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatal(err)
	}
	etag := w.Header().Get("ETag")

	w = do(http.MethodGet, "/v1/templates/"+created.ID, "")
	if w.Code != 200 || w.Header().Get("ETag") == "" {
		t.Fatalf("get: %d", w.Code)
	}
	w = do(http.MethodPost, "/v1/templates/"+created.ID+"/preview", `{"variables":{"name":"Ada"}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Hello Ada") {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}

	// Seed more rows for cursor + page list.
	svc := m.Service()
	ctx := asOrg(org, tenancy.RoleOwner)
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, CreateInput{Name: fmt.Sprintf("extra-%d", i), Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	w = do(http.MethodGet, "/v1/templates?limit=2&sort=name", "")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var list List
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.NextCursor == nil || len(list.Data) != 2 {
		t.Fatalf("list body: %+v %v", list, err)
	}
	w = do(http.MethodGet, "/v1/templates?limit=2&sort=name&cursor="+*list.NextCursor, "")
	if w.Code != 200 {
		t.Fatalf("list cursor: %d %s", w.Code, w.Body.String())
	}
	w = do(http.MethodGet, "/v1/templates?page=1&limit=10&includeTotal=true&sort=-createdAt", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total"`) {
		t.Fatalf("page total: %d %s", w.Code, w.Body.String())
	}

	patch := `{"subject":"Sub {{name}}","locale":"it","timezone":"Europe/Rome"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/templates/"+created.ID, strings.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", etag)
	req = req.WithContext(tenancy.WithCaller(req.Context(), caller))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	var patched Template
	_ = json.Unmarshal(w.Body.Bytes(), &patched)
	if patched.CurrentVersion < 2 {
		t.Fatalf("content version: %+v", patched)
	}

	// Name-only update (no content version bump).
	nameOnly := "renamed"
	upd, err := svc.Update(ctx, created.ID, UpdateInput{Name: &nameOnly}, formatETag(patched.Version))
	if err != nil || upd.CurrentVersion != patched.CurrentVersion {
		t.Fatalf("name-only: %+v %v", upd, err)
	}

	del := httptest.NewRequest(http.MethodDelete, "/v1/templates/"+created.ID, nil)
	del.Header.Set("If-Match", formatETag(upd.Version))
	del = del.WithContext(tenancy.WithCaller(del.Context(), caller))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, del)
	if w.Code != http.StatusNoContent && w.Code != 200 && w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	// Feature off → create refused (settings collection fallback when Settings svc is nil).
	_, err = db.Collection(settings.SettingsCollection).InsertOne(context.Background(), settings.Doc{
		ID: settings.OrgDocID(org),
		Switches: map[string]settings.SwitchValue{
			"sending.templates": {Value: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Settings = nil
	_, err = svc.Create(ctx, CreateInput{Name: "blocked", Text: "x"})
	if errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("feature off: %v", err)
	}

	orgB := store.NewID(store.PrefixOrganization)
	svcB := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test", Log: quiet}
	z, err := svcB.Create(asOrg(orgB, tenancy.RoleOwner), CreateInput{Name: "z", Text: "z"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svcB.Delete(asOrg(orgB, tenancy.RoleOwner), z.ID, `"v99"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("version mismatch delete: %v", err)
	}
	v1 := 1
	prev, err := svcB.Preview(asOrg(orgB, tenancy.RoleOwner), z.ID, PreviewInput{
		Version: &v1, Variables: map[string]any{}, Locale: "en", Timezone: "UTC",
	})
	if err != nil || prev.Text != "z" {
		t.Fatalf("preview version: %+v %v", prev, err)
	}
	badVer := 99
	if _, err := svcB.RenderForSubmit(asOrg(orgB, tenancy.RoleOwner), z.ID, &badVer, nil); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatalf("missing version: %v", err)
	}
}
