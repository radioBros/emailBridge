//go:build integration

package audit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
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
		URI: uri, Database: fmt.Sprintf("ebtest_aud_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestIntegrationWriteListFiltersPagingIsolation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := &Writer{DB: db, Log: quiet, Retention: 24 * time.Hour}

	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	write := func(org, action, targetID string) {
		cctx := tenancy.WithCaller(ctx, tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: "usr_actor", OrganizationID: org, Role: tenancy.RoleAdmin,
		})
		cctx = WithUserAgent(cctx, "test-agent")
		if err := w.Record(cctx, action, Target{Type: "account", ID: targetID}, Changes{
			"name": {Before: "a", After: "b"},
			"pass": {Before: Secret("x"), After: Secret("y")},
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(orgA, "account.update", "acc_1")
	write(orgA, "account.create", "acc_2")
	write(orgB, "account.update", "acc_3")

	// instance-level
	if err := w.Record(ctx, "settings.update", Target{Type: "settings", ID: "global"}, nil); err != nil {
		t.Fatal(err)
	}

	listA := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_a", OrganizationID: orgA, Role: tenancy.RoleAdmin,
	})
	res, err := w.List(listA, ListFilter{Limit: 10, Sort: "-at"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 2 {
		t.Fatalf("org A sees %d (want 2, no org B / no instance)", len(res.Data))
	}
	for _, r := range res.Data {
		if r.OrganizationID == nil || *r.OrganizationID != orgA {
			t.Fatalf("leaked record: %+v", r)
		}
		if r.Changes["pass"].After != redacted {
			t.Fatalf("secret stored: %+v", r.Changes["pass"])
		}
		if r.ExpireAt == nil {
			t.Fatal("expireAt missing")
		}
		if r.Actor.UserAgent != "test-agent" {
			t.Fatalf("ua: %q", r.Actor.UserAgent)
		}
	}

	// filter by action
	res, err = w.List(listA, ListFilter{Action: "account.create", Limit: 10})
	if err != nil || len(res.Data) != 1 || res.Data[0].Target.ID != "acc_2" {
		t.Fatalf("action filter: %+v %v", res, err)
	}

	// target filter
	res, err = w.List(listA, ListFilter{TargetID: "acc_1", Limit: 10})
	if err != nil || len(res.Data) != 1 {
		t.Fatalf("target filter: %+v %v", res, err)
	}

	// instance owner sees org + instance-level
	ownerCtx := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_own", OrganizationID: orgA,
		Role: tenancy.RoleOwner, InstanceOwner: true,
	})
	res, err = w.List(ownerCtx, ListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 3 {
		t.Fatalf("owner sees %d want 3", len(res.Data))
	}

	// org B isolation
	listB := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_b", OrganizationID: orgB, Role: tenancy.RoleAdmin,
	})
	res, err = w.List(listB, ListFilter{Limit: 10})
	if err != nil || len(res.Data) != 1 || res.Data[0].Target.ID != "acc_3" {
		t.Fatalf("org B: %+v %v", res, err)
	}

	// paging
	for i := 0; i < 5; i++ {
		write(orgA, "account.touch", fmt.Sprintf("acc_p_%d", i))
	}
	page1, err := w.List(listA, ListFilter{Limit: 3, Sort: "-at"})
	if err != nil || len(page1.Data) != 3 || page1.NextCursor == nil {
		t.Fatalf("page1: %+v %v", page1, err)
	}
	page2, err := w.List(listA, ListFilter{Limit: 3, Cursor: *page1.NextCursor, Sort: "-at"})
	if err != nil || len(page2.Data) == 0 {
		t.Fatalf("page2: %+v %v", page2, err)
	}
	if page1.Data[0].ID == page2.Data[0].ID {
		t.Fatal("cursor did not advance")
	}

	// page mode + total
	res, err = w.List(listA, ListFilter{Page: 1, Limit: 2, IncludeTotal: true, Sort: "-at"})
	if err != nil || res.Total == nil || *res.Total < 2 {
		t.Fatalf("page total: %+v %v", res, err)
	}

	// unauthenticated
	if _, err := w.List(ctx, ListFilter{}); err == nil {
		t.Fatal("expected auth error")
	}

	// actor filter + from/to + id sort
	res, err = w.List(listA, ListFilter{ActorID: "usr_actor", Limit: 5, Sort: "-id"})
	if err != nil || len(res.Data) == 0 {
		t.Fatalf("actor: %+v %v", res, err)
	}
	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)
	if _, err = w.List(listA, ListFilter{From: &from, To: &to, Limit: 5, Sort: "at"}); err != nil {
		t.Fatal(err)
	}

	// API key actor path
	keyCtx := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: orgA, Scopes: []string{"audit:read"},
	})
	if err := w.Record(keyCtx, "apikey.action", Target{Type: "x", ID: "1"}, nil); err != nil {
		t.Fatal(err)
	}
	sysCtx := tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: orgA})
	if err := w.Record(sysCtx, "system.action", Target{Type: "x", ID: "2"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationModuleAndHTTP(t *testing.T) {
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
	m := &Module{Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "audit" || m.Writer() == nil || m.Stop(ctx) != nil {
		t.Fatal("module")
	}
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	_ = m.Writer().Record(cctx, "t.a", Target{Type: "t", ID: "1"}, nil)

	r := httptest.NewRequest(http.MethodGet, "/v1/audit-log?limit=5&actorId=x&action=t.a", nil)
	r = r.WithContext(cctx)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("http %d %s", w.Code, w.Body.String())
	}
	if _, err := m.list(cctx, &listInput{From: "2020-01-01T00:00:00Z", To: "2099-01-01T00:00:00.000000000Z", Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.list(cctx, &listInput{From: "bad"}); err == nil {
		t.Fatal("bad from")
	}
	if _, err := m.list(cctx, &listInput{To: "bad"}); err == nil {
		t.Fatal("bad to")
	}
	owner := tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_o", InstanceOwner: true, Role: tenancy.RoleOwner})
	if _, err := m.Writer().List(owner, ListFilter{Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
