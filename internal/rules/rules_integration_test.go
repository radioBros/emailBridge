//go:build integration

package rules

import (
	"context"
	"encoding/json"
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
		URI: uri, Database: fmt.Sprintf("ebtest_rules_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
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

func TestIntegrationCRUDVersionsIsolationAndTest(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true, app.RoleWebhook: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)

	endpoints := map[string]bool{"whe_01jb0000000000000000000001": true}
	m := &Module{
		EndpointExists: func(_ context.Context, _, id string) (bool, error) { return endpoints[id], nil },
		EnvLoader: func(context.Context, string) (map[string]string, []string, error) {
			return map[string]string{"webhookKey": "sekrit"}, []string{"webhookKey"}, nil
		},
	}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "rules" || m.Engine() == nil || m.Repo() == nil || m.Stop(ctx) != nil {
		t.Fatal("module identity")
	}

	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	ep := "whe_01jb0000000000000000000001"

	asOrg := func(org string, role tenancy.Role) context.Context {
		return tenancy.WithCaller(ctx, tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: role,
		})
	}

	r1, err := m.repo.Create(asOrg(orgA, tenancy.RoleAdmin), RouteCreate{
		Name: "pec", EndpointID: ep, Filter: `type == "message.received" && hasHeader("x-ricevuta")`,
		Map: `{"key": env.webhookKey, "receipt": header("x-ricevuta")}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r1.CurrentVersion != 1 || r1.Filter == "" {
		t.Fatalf("%+v", r1)
	}
	// name unique
	if _, err := m.repo.Create(asOrg(orgA, tenancy.RoleAdmin), RouteCreate{Name: "pec", EndpointID: ep, Filter: "true"}); err == nil {
		t.Fatal("expected name taken")
	}
	// tenant isolation: org B cannot see org A route
	if _, err := m.repo.Get(asOrg(orgB, tenancy.RoleAdmin), r1.ID); err == nil {
		t.Fatal("expected not found for other org")
	}
	// create in org B with same name is fine
	if _, err := m.repo.Create(asOrg(orgB, tenancy.RoleAdmin), RouteCreate{Name: "pec", EndpointID: ep, Filter: "true"}); err != nil {
		t.Fatal(err)
	}

	filter2 := `type == "message.received"`
	map2 := `{"ok": true}`
	name2 := "pec-renamed"
	en := false
	ord := 7
	bld := map[string]any{"match": "all"}
	hdrs := map[string]any{"X-A": "1"}
	pl := map[string]any{"text": "plain"}
	upd, err := m.repo.Update(asOrg(orgA, tenancy.RoleAdmin), r1.ID, RouteUpdate{
		Name: &name2, Enabled: &en, Filter: &filter2, Map: &map2, Builder: &bld,
		Headers: &hdrs, Payload: &pl, Order: &ord, EndpointID: &ep,
	}, formatETag(r1.Version))
	if err != nil {
		t.Fatal(err)
	}
	if upd.CurrentVersion != 2 || upd.Order != 7 || upd.Enabled {
		t.Fatalf("version bump: %+v", upd)
	}
	var verCount int64
	verCount, err = db.Collection(RouteVersionsCollection).CountDocuments(ctx, map[string]any{"routeId": r1.ID})
	if err != nil || verCount != 2 {
		t.Fatalf("versions=%d err=%v", verCount, err)
	}

	// restore map that references a secret env value, then test masking
	secretMap := `{"key": env["webhookKey"]}`
	upd, err = m.repo.Update(asOrg(orgA, tenancy.RoleAdmin), r1.ID, RouteUpdate{Map: &secretMap}, formatETag(upd.Version))
	if err != nil {
		t.Fatal(err)
	}
	ev := sampleEvent()
	res, err := m.repo.Test(asOrg(orgA, tenancy.RoleAdmin), r1.ID, RouteTestRequest{Event: ev})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("test eval error: %s", res.Error)
	}
	if !res.Matched || res.Mapped == nil {
		t.Fatalf("%+v", res)
	}
	mapped, _ := res.Mapped.(map[string]any)
	if mapped["key"] != secretMask {
		t.Fatalf("secret not masked: %#v full=%#v", mapped["key"], mapped)
	}

	// second route for cursor pagination
	r2, err := m.repo.Create(asOrg(orgA, tenancy.RoleAdmin), RouteCreate{Name: "other", EndpointID: ep, Filter: "true"})
	if err != nil {
		t.Fatal(err)
	}
	_ = r2
	// list cursor
	list, err := m.repo.List(asOrg(orgA, tenancy.RoleViewer), ListFilter{Limit: 1, Sort: "-id"})
	if err != nil || len(list.Data) != 1 || list.NextCursor == nil {
		t.Fatalf("%+v %v", list, err)
	}
	list2, err := m.repo.List(asOrg(orgA, tenancy.RoleViewer), ListFilter{Limit: 10, Cursor: *list.NextCursor, Sort: "-id"})
	if err != nil || len(list2.Data) < 1 {
		t.Fatalf("cursor page: %+v %v", list2, err)
	}
	list, err = m.repo.List(asOrg(orgA, tenancy.RoleViewer), ListFilter{Page: 1, Limit: 10, IncludeTotal: true, Sort: "name"})
	if err != nil || list.Total == nil || *list.Total < 2 {
		t.Fatalf("%+v %v", list, err)
	}

	// HTTP path smoke
	call := func(method, path, body string, org string, hdr map[string]string) (int, map[string]any, http.Header) {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		r = r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
		}))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Header()
	}
	code, body, _ := call(http.MethodGet, "/v1/routes/"+r1.ID, "", orgA, nil)
	if code != 200 || body["id"] != r1.ID {
		t.Fatalf("get http: %d %v", code, body)
	}
	code, body, _ = call(http.MethodGet, "/v1/routes?limit=10&sort=-name", "", orgA, nil)
	if code != 200 {
		t.Fatalf("list http: %d %v", code, body)
	}
	code, body, _ = call(http.MethodPost, "/v1/routes/"+r1.ID+"/test", `{"event":{"type":"message.received","data":{"headers":{"x-ricevuta":["accettazione"]}}}}`, orgA, nil)
	if code != 200 {
		t.Fatalf("test http: %d %v", code, body)
	}
	code, body, hdr := call(http.MethodPost, "/v1/routes", `{"name":"http-create","endpointId":"`+ep+`","filter":"true"}`, orgA, nil)
	if code != 201 && code != 200 {
		t.Fatalf("create http: %d %v", code, body)
	}
	_ = hdr
	newID, _ := body["id"].(string)
	if newID != "" {
		code, _, _ = call(http.MethodPatch, "/v1/routes/"+newID, `{"enabled":false}`, orgA, map[string]string{"If-Match": formatETag(1)})
		if code != 200 {
			t.Fatalf("patch http: %d", code)
		}
		code, _, _ = call(http.MethodDelete, "/v1/routes/"+newID, "", orgA, map[string]string{"If-Match": formatETag(2)})
		if code != 204 && code != 200 {
			t.Fatalf("delete http: %d", code)
		}
	}

	// missing endpoint
	if _, err := m.repo.Create(asOrg(orgA, tenancy.RoleAdmin), RouteCreate{Name: "noep", EndpointID: "whe_01jb0000000000000000000099", Filter: "true"}); err == nil {
		t.Fatal("expected endpoint not found")
	}
	// bad compile on create
	if _, err := m.repo.Create(asOrg(orgA, tenancy.RoleAdmin), RouteCreate{Name: "bad", EndpointID: ep, Filter: "!!!"}); err == nil {
		t.Fatal("expected compile error")
	}
	// version mismatch
	if _, err := m.repo.Update(asOrg(orgA, tenancy.RoleAdmin), r1.ID, RouteUpdate{}, `"v999"`); err == nil {
		t.Fatal("expected version mismatch")
	}
	// eventId not found on test
	if _, err := m.repo.Test(asOrg(orgA, tenancy.RoleAdmin), r1.ID, RouteTestRequest{EventID: "evt_01jb0000000000000000000001"}); err == nil {
		t.Fatal("expected event not found")
	}

	if err := m.repo.Delete(asOrg(orgA, tenancy.RoleAdmin), r1.ID, formatETag(upd.Version)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.repo.Get(asOrg(orgA, tenancy.RoleAdmin), r1.ID); err == nil {
		t.Fatal("deleted")
	}

	// rule error writer
	if err := m.repo.WriteRuleError(ctx, RuleError{
		OrganizationID: orgA, RouteID: "rte_x", Version: 1, Phase: "filter", Message: "boom",
		PayloadExcerpt: strings.Repeat("x", 5000),
	}); err != nil {
		t.Fatal(err)
	}

	// defaultEndpointExists against empty collection
	ok, err := defaultEndpointExists(db)(ctx, orgA, "whe_01jb0000000000000000000001")
	if err != nil || ok {
		t.Fatalf("default exists: %v %v", ok, err)
	}
	// insert a fake endpoint doc and re-check
	_, _ = db.Collection(EndpointsCollection).InsertOne(ctx, map[string]any{
		"_id": "whe_01jb0000000000000000000001", "organizationId": orgA,
	})
	ok, err = defaultEndpointExists(db)(ctx, orgA, "whe_01jb0000000000000000000001")
	if err != nil || !ok {
		t.Fatalf("default exists after insert: %v %v", ok, err)
	}
	m2 := &Module{} // uses defaults
	m2.EndpointExists = nil
	repo2 := &Repo{DB: db, Engine: m.engine}
	if repo2.endpoints() == nil {
		t.Fatal("default endpoints")
	}
	_ = repo2.events()
}

func TestDefaultEndpointExistsNilDB(t *testing.T) {
	ok, err := defaultEndpointExists(nil)(context.Background(), "org", "whe_x")
	if err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
}
