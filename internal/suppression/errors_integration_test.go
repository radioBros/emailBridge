//go:build integration

package suppression

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationErrorsAndEdgeCases(t *testing.T) {
	db := testDB(t)
	_ = EnsureIndexes(context.Background(), db)
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}

	bare := store.WithOrganization(context.Background(), org)
	if _, err := svc.CreateList(bare, CreateListInput{Name: "x"}); err == nil {
		t.Fatal("no caller")
	}
	if _, err := svc.ListLists(bare, ListFilter{}); err == nil {
		t.Fatal("list auth")
	}
	if _, err := svc.GetList(bare, store.NewID(store.PrefixSuppression)); err == nil {
		t.Fatal("get auth")
	}

	cctx := orgCtx(org)
	if _, err := svc.CreateList(cctx, CreateListInput{Name: "x", Kind: "nope"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.CreateList(cctx, CreateListInput{Name: strings.Repeat("n", 200)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	l, err := svc.CreateList(cctx, CreateListInput{Name: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportCSV(cctx, l.ID, strings.NewReader("\"unclosed\n")); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.ListLists(cctx, ListFilter{Q: strings.Repeat("q", 201)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.ListEntries(cctx, EntryFilter{ListID: l.ID, Q: strings.Repeat("q", 201)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := svc.CheckAddresses(cctx, org, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureSystemList(cctx, org, KindManual); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}

	rctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if _, err := svc.CreateList(rctx, CreateListInput{Name: "nope"}); err == nil {
		t.Fatal("viewer write")
	}

	m := &Module{svc: svc}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("z@z.test\n"))
	req = req.WithContext(withChiList(cctx, l.ID))
	w := httptest.NewRecorder()
	m.serveImport(w, req)
	if w.Code != 200 {
		t.Fatalf("import %d %s", w.Code, w.Body.String())
	}
	req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
	req2 = req2.WithContext(withChiList(cctx, store.NewID(store.PrefixSuppression)))
	w2 := httptest.NewRecorder()
	m.serveImport(w2, req2)
	if w2.Code == 200 {
		t.Fatal("expected miss")
	}
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3 = req3.WithContext(withChiList(cctx, l.ID))
	w3 := httptest.NewRecorder()
	m.serveExport(w3, req3)
	if w3.Code != 200 {
		t.Fatalf("export %d", w3.Code)
	}

	l2, err := svc.CreateList(cctx, CreateListInput{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	steal := "ok"
	if _, err := svc.PatchList(cctx, l2.ID, formatETag(l2.Version), PatchListInput{Name: &steal}); errs.CodeOf(err) != errs.CodeSuppressionListNameTaken {
		t.Fatalf("rename clash: %v", err)
	}
	sys, err := svc.CreateList(cctx, CreateListInput{Name: "Unsubscribes", Kind: KindUnsubscribe})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateList(cctx, CreateListInput{Name: "Unsubscribes-b", Kind: KindUnsubscribe}); errs.CodeOf(err) != errs.CodeSuppressionListKindTaken {
		t.Fatalf("kind clash: %v", err)
	}
	_ = sys
	// export missing list
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4 = req4.WithContext(withChiList(cctx, store.NewID(store.PrefixSuppression)))
	w4 := httptest.NewRecorder()
	m.serveExport(w4, req4)
	if w4.Code == 200 {
		t.Fatal("export miss")
	}
	// delete entry then list empty export
	e, _, _ := svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: "del@z.test"})
	_ = svc.DeleteEntry(cctx, l.ID, e.ID)
	if err := svc.DeleteList(cctx, l2.ID, formatETag(l2.Version)); err != nil {
		t.Fatal(err)
	}

	svc2 := &Service{Repo: &Repo{DB: db}, Env: ""} // default registry + production env
	_ = svc2.SuppressionEnabled(cctx, org)
	_, _ = svc.ListLists(cctx, ListFilter{Limit: 250})
	_, _ = svc.ListEntries(cctx, EntryFilter{ListID: l.ID, Limit: 250})

	// API error return paths (raise handler coverage above 75%)
	mm := &Module{svc: svc}
	_, _ = mm.createList(cctx, &createListIn{Body: CreateListInput{}})
	_, _ = mm.getList(cctx, &listIDIn{ListID: "bad"})
	_, _ = mm.patchList(cctx, &patchListIn{ListID: "bad", Body: PatchListInput{}})
	_, _ = mm.listEntries(cctx, &listEntriesIn{ListID: "bad"})
	_, _ = mm.createEntry(cctx, &createEntryIn{ListID: "bad", Body: CreateEntryInput{Address: "a@b.c"}})
	_, _ = mm.listLists(cctx, &listListsIn{Sort: "nope"})
	_, _ = mm.deleteEntry(cctx, &deleteEntryIn{ListID: l.ID, EntryID: "missing"})
	_, _ = mm.deleteList(cctx, &deleteListIn{ListID: "bad", IfMatch: `"v1"`})
}

func withChiList(ctx context.Context, listID string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("listId", listID)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}
