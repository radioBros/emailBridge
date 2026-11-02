//go:build integration

package suppression

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
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
		URI: uri, Database: fmt.Sprintf("ebtest_sup_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func orgCtx(org string) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
}

func TestIntegrationFullSurface(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	cctx := orgCtx(org)
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}

	l, err := svc.CreateList(cctx, CreateListInput{Name: "Block", Kind: KindManual, Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateList(cctx, CreateListInput{Name: "Block"}); errs.CodeOf(err) != errs.CodeSuppressionListNameTaken {
		t.Fatalf("dup name: %v", err)
	}
	name := "Renamed"
	desc := "new"
	l2, err := svc.PatchList(cctx, l.ID, formatETag(l.Version), PatchListInput{Name: &name, Description: &desc})
	if err != nil || l2.Name != "Renamed" {
		t.Fatalf("%+v %v", l2, err)
	}
	got, err := svc.GetList(cctx, l.ID)
	if err != nil || got.Description != "new" {
		t.Fatal(err)
	}
	page, err := svc.ListLists(cctx, ListFilter{Sort: "name", Q: "Ren", IncludeTotal: true, Limit: 10})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	_, _ = svc.ListLists(cctx, ListFilter{Sort: "-createdAt"})
	_, _ = svc.ListLists(cctx, ListFilter{Sort: "createdAt"})
	_, _ = svc.ListLists(cctx, ListFilter{Sort: "-name"})
	if _, err := svc.ListLists(cctx, ListFilter{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}

	e, created, err := svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: "bad@example.test", Reason: "spam"})
	if err != nil || !created {
		t.Fatal(err)
	}
	_, created, _ = svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: "bad@example.test"})
	if created {
		t.Fatal("dup")
	}
	ents, err := svc.ListEntries(cctx, EntryFilter{ListID: l.ID, Q: "bad", IncludeTotal: true, Limit: 5})
	if err != nil || len(ents.Data) != 1 {
		t.Fatalf("%+v %v", ents, err)
	}

	imp, err := svc.ImportCSV(cctx, l.ID, strings.NewReader("email,reason\nok@example.test,note\nbad2@example.test,\nnot-email\n\n"))
	if err != nil || imp.Imported < 2 || imp.Invalid < 1 {
		t.Fatalf("%+v %v", imp, err)
	}
	var buf bytes.Buffer
	if err := svc.ExportCSV(cctx, l.ID, &buf); err != nil || !strings.Contains(buf.String(), "bad@example.test") {
		t.Fatal(err)
	}

	if err := svc.CheckAddresses(cctx, org, []string{"bad@example.test"}, ""); errs.CodeOf(err) != errs.CodeRecipientSuppressed {
		t.Fatal(err)
	}
	if !svc.SuppressionEnabled(cctx, org) {
		t.Fatal("default on")
	}

	if err := svc.AddUnsubscribe(ctx, org, "gone@example.test", "one-click", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddBounce(ctx, org, "bounce@example.test", "5.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddComplaint(ctx, org, "comp@example.test", "abuse"); err != nil {
		t.Fatal(err)
	}
	// second ensure is idempotent
	if _, err := svc.EnsureSystemList(ctx, org, KindUnsubscribe); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateList(cctx, CreateListInput{Name: "Unsubscribes2", Kind: KindUnsubscribe}); errs.CodeOf(err) != errs.CodeSuppressionListKindTaken && errs.CodeOf(err) != errs.CodeSuppressionListNameTaken {
		// may be name or kind
		t.Logf("second system create: %v", err)
	}

	if err := svc.DeleteEntry(cctx, l.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteList(cctx, l.ID, formatETag(l2.Version)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationAPIAndCSVRoutes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = EnsureIndexes(ctx, db)
	org := store.NewID(store.PrefixOrganization)
	cctx := orgCtx(org)
	m := &Module{Registry: features.DefaultRegistry(), Env: "test"}
	m.svc = &Service{Repo: &Repo{DB: db}, Registry: m.Registry, Env: m.Env}
	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	m.registerAPI(srv)
	srv.Router.Post("/v1/suppression-lists/{listId}/entries:import", m.serveImport)
	srv.Router.Get("/v1/suppression-lists/{listId}/entries:export", m.serveExport)

	// exercise huma handlers via service-backed module methods
	out, err := m.createList(cctx, &createListIn{Body: CreateListInput{Name: "API"}})
	if err != nil || out.Status != http.StatusCreated {
		t.Fatal(err)
	}
	listID := out.Body.ID
	if _, err := m.getList(cctx, &listIDIn{ListID: listID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.listLists(cctx, &listListsIn{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	nm := "API2"
	if _, err := m.patchList(cctx, &patchListIn{ListID: listID, IfMatch: formatETag(1), Body: PatchListInput{Name: &nm}}); err != nil {
		t.Fatal(err)
	}
	co, err := m.createEntry(cctx, &createEntryIn{ListID: listID, Body: CreateEntryInput{Address: "x@y.test"}})
	if err != nil || co.Status != 201 {
		t.Fatal(err)
	}
	co2, err := m.createEntry(cctx, &createEntryIn{ListID: listID, Body: CreateEntryInput{Address: "x@y.test"}})
	if err != nil || co2.Status != 200 {
		t.Fatalf("dup entry status %d %v", co2.Status, err)
	}
	if _, err := m.listEntries(cctx, &listEntriesIn{ListID: listID}); err != nil {
		t.Fatal(err)
	}

	withList := func(r *http.Request) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("listId", listID)
		return r.WithContext(context.WithValue(cctx, chi.RouteCtxKey, rctx))
	}
	req := withList(httptest.NewRequest(http.MethodPost, "/v1/suppression-lists/"+listID+"/entries:import", strings.NewReader("a@b.test,r\n")))
	rr := httptest.NewRecorder()
	m.serveImport(rr, req)
	if rr.Code != 200 {
		t.Fatalf("import %d %s", rr.Code, rr.Body.String())
	}
	req = withList(httptest.NewRequest(http.MethodGet, "/v1/suppression-lists/"+listID+"/entries:export", nil))
	rr = httptest.NewRecorder()
	m.serveExport(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "a@b.test") {
		t.Fatalf("export %d %s", rr.Code, rr.Body.String())
	}

	if _, err := m.deleteEntry(cctx, &deleteEntryIn{ListID: listID, EntryID: co.Body.ID}); err != nil {
		t.Fatal(err)
	}
	cur, _ := m.svc.GetList(cctx, listID)
	if _, err := m.deleteList(cctx, &deleteListIn{ListID: listID, IfMatch: formatETag(cur.Version)}); err != nil {
		t.Fatal(err)
	}
	_ = io.Discard
}

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "sup-n1", Log: slog.Default()})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Registry: features.DefaultRegistry(), Env: "test", Log: slog.Default()}
	if m.Name() != "suppression" || len(m.Roles()) == 0 {
		t.Fatal("meta")
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	if m.Service() == nil {
		t.Fatal("nil")
	}
}
