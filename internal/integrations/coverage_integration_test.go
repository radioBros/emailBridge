//go:build integration

package integrations

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type failEndpoints struct {
	*fakeEndpoints
	failCreate bool
}

func (f *failEndpoints) Create(ctx context.Context, spec EndpointSpec) (*CreatedEndpoint, error) {
	if f.failCreate {
		return nil, errors.New("create failed")
	}
	return f.fakeEndpoints.Create(ctx, spec)
}

func TestCoverageCursorListAndRollback(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	eps := newFakeEndpoints()
	rts := newFakeRoutes()
	sts := newFakeSettings()
	ping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ping.Close()

	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher,
		Endpoints: eps, Routes: rts, Settings: sts, HTTP: ping.Client(),
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)

	for _, name := range []string{"A-zap", "B-zap", "C-zap"} {
		_, _, err := svc.Install(ctx, TypeZapier, InstallInput{
			Name:   name,
			Config: map[string]any{"hookUrl": ping.URL},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	page1, err := svc.List(ctx, ListFilter{Limit: 2, Sort: "name"})
	if err != nil || len(page1.Data) != 2 || page1.NextCursor == nil {
		t.Fatalf("%+v %v", page1, err)
	}
	page2, err := svc.List(ctx, ListFilter{Limit: 2, Sort: "name", Cursor: *page1.NextCursor})
	if err != nil || len(page2.Data) != 1 {
		t.Fatalf("%+v %v", page2, err)
	}
	if _, err := svc.List(ctx, ListFilter{Cursor: "%%%", Sort: "name"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	q := "zzzz-none"
	empty, err := svc.List(ctx, ListFilter{Q: q, Page: 1, IncludeTotal: true})
	if err != nil || len(empty.Data) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}

	// rollback when endpoint create fails
	bad := &failEndpoints{fakeEndpoints: newFakeEndpoints(), failCreate: true}
	svc.Endpoints = bad
	if _, _, err := svc.Install(ctx, TypeSlack, InstallInput{
		Config: map[string]any{"webhookUrl": ping.URL},
	}); err == nil {
		t.Fatal("expected fail")
	}

	svc.Endpoints = eps
	// duplicate name
	if _, _, err := svc.Install(ctx, TypeZapier, InstallInput{
		Name: "A-zap", Config: map[string]any{"hookUrl": ping.URL},
	}); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("dup: %v", err)
	}

	// rspamd patch secrets + reapply settings
	rz, _, err := svc.Install(ctx, TypeRspamd, InstallInput{
		Name:    "rs1",
		Config:  map[string]any{"baseUrl": ping.URL},
		Secrets: map[string]string{"password": "one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	patched, err := svc.Patch(ctx, rz.ID, formatETag(rz.Version), PatchInput{
		Config:  map[string]any{"baseUrl": ping.URL + "/v2", "timeout": "8s"},
		Secrets: map[string]string{"password": "two"},
	})
	if err != nil || !patched.SecretsSet["password"] {
		t.Fatalf("%+v %v", patched, err)
	}
	if sts.values["integrations.rspamd.baseUrl"] != ping.URL+"/v2" {
		t.Fatal(sts.values)
	}
	tr, err := svc.Test(ctx, patched.ID)
	if err != nil || !tr.OK {
		t.Fatalf("%+v %v", tr, err)
	}

	// version mismatch
	if _, err := svc.Patch(ctx, patched.ID, `"v1"`, PatchInput{}); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if err := svc.Uninstall(ctx, patched.ID, `"v1"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}

	// API error paths
	m := &Module{svc: svc}
	if _, err := m.catalogue(orgCtx(org, tenancy.RoleViewer), &catalogueIn{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.install(orgCtx(org, tenancy.RoleViewer), &installIn{Type: TypeZapier, Body: InstallInput{Config: map[string]any{"hookUrl": ping.URL}}}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	if _, err := m.get(ctx, &idIn{InstanceID: "int_00000000000000000000000000"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := m.list(ctx, &listIn{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	if _, err := m.patch(ctx, &patchIn{InstanceID: "int_00000000000000000000000000", Body: PatchInput{}}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := m.delete(ctx, &deleteIn{InstanceID: "int_00000000000000000000000000"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := m.test(ctx, &idIn{InstanceID: "int_00000000000000000000000000"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	// install without resources adapters (settings-only path already covered; slack without adapters)
	svc2 := &Service{Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher}
	v, _, err := svc2.Install(ctx, TypeSlack, InstallInput{
		Name:   "no-resources",
		Config: map[string]any{"webhookUrl": ping.URL},
	})
	if err != nil || len(v.CreatedResources.Endpoints) != 0 {
		t.Fatalf("%+v %v", v, err)
	}
	_ = svc2.Uninstall(ctx, v.ID, formatETag(v.Version))
}

func TestCoverageModuleStartVariants(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, db)
	app.Provide(a, cipher)
	reg := features.DefaultRegistry()
	app.Provide(a, reg)
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Registry: DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = m.Name()
	_ = m.Roles()
	_ = m.Stop(context.Background())
	// second start with same switches (skip existing)
	m2 := &Module{Registry: DefaultRegistry()}
	a2 := app.New(app.Options{Roles: app.RoleSet{}})
	app.Provide(a2, db)
	app.Provide(a2, cipher)
	app.Provide(a2, reg)
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageCreateResourcesRouteIndex(t *testing.T) {
	svc := &Service{
		Endpoints: newFakeEndpoints(),
		Routes:    newFakeRoutes(),
	}
	def := &Definition{
		Resources: ResourceTemplates{
			Routes: []RouteTemplate{{Name: "r", EndpointIndex: 0, Filter: "true"}},
		},
	}
	_, err := svc.createResources(context.Background(), def, "int_01htestxxxxxxxxxxxxxxx0001", nil, nil)
	if err == nil {
		t.Fatal("expected index error")
	}
}

func TestCoverageQTooLong(t *testing.T) {
	db := testDB(t)
	svc := &Service{Repo: &Repo{DB: db}, Registry: DefaultRegistry()}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)
	long := strings.Repeat("a", 201)
	if _, err := svc.List(ctx, ListFilter{Q: long}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.Patch(orgCtx(org, tenancy.RoleViewer), "x", "", PatchInput{}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	if err := svc.Uninstall(orgCtx(org, tenancy.RoleViewer), "x", ""); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	if _, err := svc.Test(orgCtx(org, tenancy.RoleViewer), "x"); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	_ = tenancy.RoleOwner
}
