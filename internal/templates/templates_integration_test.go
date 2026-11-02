//go:build integration

package templates

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_tpl_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(27).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func asOrg(org string, role tenancy.Role) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: role,
	})
}

func TestIntegrationCRUDPreviewVersions(t *testing.T) {
	db := testDB(t)
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "production",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) },
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := asOrg(org, tenancy.RoleOwner)

	tpl, err := svc.Create(ctx, CreateInput{
		Name: "welcome", Subject: "Hi {{name}}", Text: "Hello {{name}}",
		HTML: "<p>Hello {{name}}</p>", Partials: map[string]string{"sig": "-- team"},
		Strict: true, Locale: "en", Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tpl.CurrentVersion != 1 || tpl.Engine != EngineHandlebars {
		t.Fatalf("%+v", tpl)
	}

	got, err := svc.Get(ctx, tpl.ID)
	if err != nil || got.Name != "welcome" {
		t.Fatalf("%v %+v", err, got)
	}
	list, err := svc.List(ctx, ListFilter{Sort: "name", Q: "wel", Limit: 10})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	prev, err := svc.Preview(ctx, tpl.ID, PreviewInput{Variables: map[string]any{"name": "Ada"}})
	if err != nil || prev.Subject != "Hi Ada" || prev.Text != "Hello Ada" {
		t.Fatalf("preview: %+v %v", prev, err)
	}
	_, err = svc.Preview(ctx, tpl.ID, PreviewInput{Variables: map[string]any{}})
	if errs.CodeOf(err) != errs.CodeTemplateVariableMissing {
		t.Fatalf("strict preview: %v", err)
	}

	name := "welcome-v2"
	html := "<p>Hey {{name}}</p>{{> sig}}"
	strict := false
	upd, err := svc.Update(ctx, tpl.ID, UpdateInput{Name: &name, HTML: &html, Strict: &strict}, formatETag(tpl.Version))
	if err != nil {
		t.Fatal(err)
	}
	if upd.CurrentVersion != 2 || upd.Version != 2 {
		t.Fatalf("version bump: %+v", upd)
	}
	v1 := 1
	old, err := svc.RenderForSubmit(ctx, tpl.ID, &v1, map[string]any{"name": "Ada"})
	if err != nil || old.HTML != "<p>Hello Ada</p>" {
		t.Fatalf("old version: %+v %v", old, err)
	}
	subj, text, htmlOut, err := svc.RenderParts(ctx, tpl.ID, nil, map[string]any{"name": "Bob"})
	if err != nil || subj != "Hi Bob" || text != "Hello Bob" || !strings.Contains(htmlOut, "Hey Bob") || !strings.Contains(htmlOut, "-- team") {
		t.Fatalf("render parts: %q %q %q %v", subj, text, htmlOut, err)
	}

	_, err = svc.Create(ctx, CreateInput{Name: "welcome-v2", Text: "x"})
	if errs.CodeOf(err) != errs.CodeTemplateNameTaken {
		t.Fatalf("dup name: %v", err)
	}
	if err := svc.Delete(ctx, tpl.ID, formatETag(upd.Version)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, tpl.ID); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatalf("deleted: %v", err)
	}
}

func TestIntegrationTenantIsolationAndModule(t *testing.T) {
	db := testDB(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test", Log: quiet}
	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	a, err := svc.Create(asOrg(orgA, tenancy.RoleOwner), CreateInput{Name: "a", Text: "A {{x}}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(asOrg(orgB, tenancy.RoleOwner), a.ID); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	m := &Module{Registry: features.DefaultRegistry(), Env: "test", Log: quiet}
	if m.Name() != "templates" || len(m.Roles()) == 0 || m.Stop(context.Background()) != nil {
		t.Fatal("module meta")
	}
	_ = a
}
