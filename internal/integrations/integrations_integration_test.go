//go:build integration

package integrations

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/sinks"
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
		URI: uri, Database: fmt.Sprintf("ebtest_int_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(35).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := crypto.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = 3
	}
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: &crypto.MongoKeyStore{DB: db}, NewID: func() string {
		return store.NewID(store.PrefixDataKey)
	}}
}

func orgCtx(org string, role tenancy.Role) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_test", OrganizationID: org, Role: role,
	})
}

func TestIntegrationLifecycle(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	eps := newFakeEndpoints()
	rts := newFakeRoutes()
	sts := newFakeSettings()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher,
		Endpoints: eps, Routes: rts, Settings: sts, HTTP: srv.Client(),
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)

	cat, err := svc.Catalogue(ctx, CatalogueFilter{Category: CategoryChat})
	if err != nil || len(cat.Data) < 1 {
		t.Fatalf("%+v %v", cat, err)
	}
	foundSlack := false
	for _, it := range cat.Data {
		if it.ID == TypeSlack {
			foundSlack = true
			break
		}
	}
	if !foundSlack {
		t.Fatalf("slack missing: %+v", cat)
	}
	cat, err = svc.Catalogue(ctx, CatalogueFilter{Q: "rspamd"})
	if err != nil || len(cat.Data) != 1 {
		t.Fatalf("q: %+v %v", cat, err)
	}

	v, tr, err := svc.Install(ctx, TypeSlack, InstallInput{
		Config:  map[string]any{"webhookUrl": srv.URL, "channelHint": "#alerts"},
		RunTest: true,
	})
	if err != nil || v == nil || tr == nil || !tr.OK {
		t.Fatalf("install slack: %v %+v %+v", err, v, tr)
	}
	if len(v.CreatedResources.Endpoints) != 1 || len(v.CreatedResources.Routes) != 4 {
		t.Fatalf("resources: %+v", v.CreatedResources)
	}
	if !sts.switches[SwitchName(TypeSlack)] {
		t.Fatal("switch not on")
	}

	list, err := svc.List(ctx, ListFilter{Page: 1, IncludeTotal: true, Sort: "name"})
	if err != nil || len(list.Data) != 1 || list.Total == nil || *list.Total != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	got, err := svc.Get(ctx, v.ID)
	if err != nil || got.Name != "Slack" {
		t.Fatalf("get: %+v %v", got, err)
	}

	off := false
	patched, err := svc.Patch(ctx, v.ID, formatETag(v.Version), PatchInput{Enabled: &off})
	if err != nil || patched.Enabled {
		t.Fatalf("disable: %+v %v", patched, err)
	}
	ep := eps.items[v.CreatedResources.Endpoints[0]]
	if ep == nil || ep.Enabled {
		t.Fatalf("endpoint still enabled: %+v", ep)
	}

	on := true
	name := "Slack prod"
	patched, err = svc.Patch(ctx, patched.ID, formatETag(patched.Version), PatchInput{
		Name: &name, Enabled: &on,
		Config: map[string]any{"webhookUrl": srv.URL + "/v2", "channelHint": "#ops", "eventsNewMail": `type == "message.received"`},
	})
	if err != nil || patched.Name != name {
		t.Fatalf("patch: %+v %v", patched, err)
	}

	tr2, err := svc.Test(ctx, patched.ID)
	if err != nil || !tr2.OK {
		t.Fatalf("test: %+v %v", tr2, err)
	}

	if err := svc.Uninstall(ctx, patched.ID, formatETag(patched.Version)); err != nil {
		t.Fatal(err)
	}
	if len(eps.items) != 0 || len(rts.items) != 0 {
		t.Fatalf("leaked resources eps=%d rts=%d", len(eps.items), len(rts.items))
	}
	if sts.switches[SwitchName(TypeSlack)] {
		t.Fatal("switch still on")
	}
	if _, err := svc.Get(ctx, patched.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestIntegrationRspamdAndZapier(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	eps := newFakeEndpoints()
	rts := newFakeRoutes()
	sts := newFakeSettings()

	ping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ping.Close()

	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher,
		Endpoints: eps, Routes: rts, Settings: sts, HTTP: ping.Client(),
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)

	rz, _, err := svc.Install(ctx, TypeRspamd, InstallInput{
		Name:    "Rspamd main",
		Config:  map[string]any{"baseUrl": ping.URL, "timeout": "3s"},
		Secrets: map[string]string{"password": "secret"},
		RunTest: true,
	})
	if err != nil || !rz.SecretsSet["password"] {
		t.Fatalf("rspamd: %+v %v", rz, err)
	}
	if sts.values["integrations.rspamd.baseUrl"] != ping.URL {
		t.Fatalf("settings: %v", sts.values)
	}

	zz, _, err := svc.Install(ctx, TypeZapier, InstallInput{
		Config: map[string]any{"hookUrl": ping.URL, "filter": "true"},
	})
	if err != nil || len(zz.CreatedResources.Endpoints) != 1 {
		t.Fatalf("zapier: %+v %v", zz, err)
	}

	if _, _, err := svc.Install(orgCtx(org, tenancy.RoleViewer), TypeSlack, InstallInput{
		Config: map[string]any{"webhookUrl": ping.URL},
	}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer install: %v", err)
	}
	if _, err := svc.Catalogue(orgCtx(org, tenancy.RoleViewer), CatalogueFilter{}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Uninstall(ctx, rz.ID, formatETag(rz.Version)); err != nil {
		t.Fatal(err)
	}
	if sts.values["integrations.rspamd.baseUrl"] != "" {
		t.Fatalf("settings not cleared: %v", sts.values)
	}
	if err := svc.Uninstall(ctx, zz.ID, formatETag(zz.Version)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationModuleAPI(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, db)
	app.Provide(a, cipher)
	app.Provide(a, features.DefaultRegistry())
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{
		Endpoints: newFakeEndpoints(), Routes: newFakeRoutes(), Settings: newFakeSettings(),
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)
	out, err := m.catalogue(ctx, &catalogueIn{Category: CategorySpam})
	if err != nil || len(out.Body.Data) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	ping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ping.Close()
	m.svc.HTTP = ping.Client()
	inst, err := m.install(ctx, &installIn{
		Type: TypeZapier,
		Body: InstallInput{Config: map[string]any{"hookUrl": ping.URL}},
	})
	if err != nil || inst.Status != http.StatusCreated {
		t.Fatalf("%+v %v", inst, err)
	}
	got, err := m.get(ctx, &idIn{InstanceID: inst.Body.ID})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := m.list(ctx, &listIn{Page: 1, IncludeTotal: true})
	if err != nil || len(listed.Body.Data) != 1 {
		t.Fatalf("%+v %v", listed, err)
	}
	name := "Z"
	patched, err := m.patch(ctx, &patchIn{
		InstanceID: inst.Body.ID, IfMatch: got.ETag, Body: PatchInput{Name: &name},
	})
	if err != nil || patched.Body.Name != "Z" {
		t.Fatalf("%+v %v", patched, err)
	}
	tr, err := m.test(ctx, &idIn{InstanceID: patched.Body.ID})
	if err != nil || !tr.Body.OK {
		t.Fatalf("%+v %v", tr, err)
	}
	if _, err := m.delete(ctx, &deleteIn{InstanceID: patched.Body.ID, IfMatch: formatETag(patched.Body.Version)}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationErrors(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	svc := &Service{Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher}
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org, tenancy.RoleOwner)
	if _, _, err := svc.Install(ctx, "nope", InstallInput{Config: map[string]any{}}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "int_00000000000000000000000000"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, ListFilter{Sort: "bogus"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
}

// An integration endpoint names its instance in sinkConfig.instanceId; delivering for organisation B must never
// run organisation A's instance (its secrets would push B's mail into A's CRM / drive).
func TestIntegrationRunActionRefusesOtherOrganisation(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher,
		Endpoints: newFakeEndpoints(), Routes: newFakeRoutes(), Settings: newFakeSettings(), HTTP: srv.Client(),
	}
	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	v, _, err := svc.Install(orgCtx(orgA, tenancy.RoleOwner), TypeSlack, InstallInput{
		Config: map[string]any{"webhookUrl": srv.URL, "channelHint": "#alerts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hits = 0
	msg := sinks.Message{Type: "message.received", Body: []byte(`{"type":"message.received","data":{"subject":"x"}}`)}
	err = svc.RunAction(context.Background(), orgB, v.ID, msg)
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("other organisation: err = %v, want not_found", err)
	}
	if err := svc.RunAction(context.Background(), "", v.ID, msg); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("empty organisation: err = %v, want not_found", err)
	}
	if hits != 0 {
		t.Fatalf("the instance's target was called %d times for a foreign organisation", hits)
	}
}
