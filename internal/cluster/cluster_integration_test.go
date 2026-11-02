//go:build integration

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type harness struct {
	t   *testing.T
	db  *store.DB
	b   *bus.Bus
	svc *Service
	mod *Module
	srv *httpapi.Server
	ctx context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_cluster_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := KPIMigration(2).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	_ = audit.Migration(1).Up(ctx, db)

	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "api-1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "api-1", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, b)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	aw := &audit.Writer{DB: db, Log: quiet}
	app.Provide(a, aw)

	m := &Module{Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })

	if err := b.RegisterNode(ctx, bus.NodeInfo{
		NodeID: "api-1", Roles: []string{"api"}, Version: "test", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.RegisterNode(ctx, bus.NodeInfo{
		NodeID: "sync-2", Roles: []string{"sync"}, Version: "test", StartedAt: time.Now().UTC(), Accounts: 2,
		Partitions: map[string]int{"relay": 1},
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Claim(ctx, scheduler.LeaderKey, []byte("api-1"), 15*time.Second)

	return &harness{t: t, db: db, b: b, svc: m.Service(), mod: m, srv: srv, ctx: ctx}
}

func (h *harness) do(method, path string, body any, c tenancy.Caller) *httptest.ResponseRecorder {
	h.t.Helper()
	var r *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r = r.WithContext(tenancy.WithCaller(h.ctx, c))
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func owner() tenancy.Caller {
	return tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_owner", OrganizationID: "org_aaaaaaaaaaaaaaaaaaaaaaaaa",
		Role: tenancy.RoleOwner, InstanceOwner: true}
}

func TestIntegrationClusterRBACAndDrain(t *testing.T) {
	h := newHarness(t)
	org := store.NewID(store.PrefixOrganization)

	cases := []struct {
		name   string
		caller tenancy.Caller
		code   int
	}{
		{"instanceOwner", owner(), 200},
		{"orgOwner", tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner}, 403},
		{"admin", tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleAdmin}, 403},
		{"viewer", tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleViewer}, 403},
		{"apiKey", tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: org, Scopes: []string{"admin"}}, 403},
	}
	for _, tc := range cases {
		w := h.do(http.MethodGet, "/v1/cluster", nil, tc.caller)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}

	w := h.do(http.MethodGet, "/v1/cluster", nil, owner())
	var view ClusterView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.NodeCount < 2 || view.Leader != "api-1" {
		t.Fatalf("cluster: %+v", view)
	}
	foundSync := false
	for _, n := range view.Nodes {
		if n.ID == "sync-2" && n.Accounts == 2 && n.Partitions["relay"] == 1 {
			foundSync = true
		}
	}
	if !foundSync {
		t.Fatalf("sync node load missing: %+v", view.Nodes)
	}

	got := make(chan string, 1)
	unsub, err := h.b.SubscribeCore(bus.ClusterDrainSubject("sync-2"), func(_ string, data []byte) {
		got <- string(data)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unsub() }()

	w = h.do(http.MethodPost, "/v1/cluster/nodes/sync-2/drain", nil, owner())
	if w.Code != http.StatusAccepted {
		t.Fatalf("drain: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("drain subject not published")
	}

	w = h.do(http.MethodPost, "/v1/cluster/nodes/missing/drain", nil, owner())
	if w.Code != 404 {
		t.Fatalf("unknown node: %d", w.Code)
	}
	w = h.do(http.MethodPost, "/v1/cluster/nodes/sync-2/drain", nil,
		tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleAdmin})
	if w.Code != 403 {
		t.Fatalf("admin drain: %d", w.Code)
	}
}

func TestIntegrationStatsTenantIsolation(t *testing.T) {
	h := newHarness(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	seed := []any{
		bson.M{"_id": "acc_a1", "organizationId": orgA, "state": "connected"},
		bson.M{"_id": "acc_a2", "organizationId": orgA, "state": "disabled"},
		bson.M{"_id": "acc_b1", "organizationId": orgB, "state": "connected"},
		bson.M{"_id": "out_a1", "organizationId": orgA, "state": "queued"},
		bson.M{"_id": "out_b1", "organizationId": orgB, "state": "sent"},
		bson.M{"_id": "dlv_a1", "organizationId": orgA, "state": "succeeded", "createdAt": now},
		bson.M{"_id": "dlv_a2", "organizationId": orgA, "state": "deadLettered", "createdAt": now},
		bson.M{"_id": "dlv_b1", "organizationId": orgB, "state": "succeeded", "createdAt": now},
		bson.M{"_id": "evt_a1", "organizationId": orgA, "type": "message.received", "time": now},
		bson.M{"_id": "evt_b1", "organizationId": orgB, "type": "message.received", "time": now},
	}
	for _, coll := range []struct {
		name string
		docs []any
	}{
		{"accounts", seed[0:3]},
		{"outboundMessages", seed[3:5]},
		{"deliveries", seed[5:8]},
		{"events", seed[8:10]},
	} {
		if _, err := h.db.Collection(coll.name).InsertMany(h.ctx, coll.docs); err != nil {
			t.Fatal(err)
		}
	}

	callerA := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: orgA, Role: tenancy.RoleViewer}
	w := h.do(http.MethodGet, "/v1/stats", nil, callerA)
	if w.Code != 200 {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	var st StatsView
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Accounts["connected"] != 1 || st.Accounts["disabled"] != 1 {
		t.Fatalf("accounts: %+v", st.Accounts)
	}
	if st.OutboundMessages["queued"] != 1 || st.OutboundMessages["sent"] != 0 {
		t.Fatalf("outbound leaked: %+v", st.OutboundMessages)
	}
	if st.DeliveriesLast24h["succeeded"] != 1 || st.DeadLetters != 1 || st.EventsLast24h != 1 {
		t.Fatalf("stats: %+v", st)
	}

	key := tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: orgA, Scopes: []string{"accounts:write"}}
	if w := h.do(http.MethodGet, "/v1/stats", nil, key); w.Code != 200 {
		t.Fatalf("api key read: %d", w.Code)
	}
}

func TestIntegrationQueuesPauseResume(t *testing.T) {
	h := newHarness(t)
	org := store.NewID(store.PrefixOrganization)
	admin := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleAdmin}

	// W72: members read the queues without the instance-wide stream statistics; scope=all is owner-only.
	w := h.do(http.MethodGet, "/v1/queues", nil, admin)
	if w.Code != 200 {
		t.Fatalf("admin queues: %d %s", w.Code, w.Body.String())
	}
	var redacted struct {
		Data []QueueView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &redacted); err != nil || len(redacted.Data) != 4 {
		t.Fatalf("admin list: %+v %v", redacted, err)
	}
	for _, q := range redacted.Data {
		if q.Pending != nil || q.AckPending != nil || q.Consumers != nil || q.Kind == "" {
			t.Fatalf("member sees stream stats or no kind: %+v", q)
		}
	}
	if w := h.do(http.MethodGet, "/v1/queues?scope=all", nil, admin); w.Code != 403 {
		t.Fatalf("admin scope=all: %d", w.Code)
	}
	viewerNoRead := tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_q", OrganizationID: org, Scopes: []string{"templates:read"}}
	if w := h.do(http.MethodGet, "/v1/queues", nil, viewerNoRead); w.Code != 403 {
		t.Fatalf("key without outbound/deliveries read: %d", w.Code)
	}
	w = h.do(http.MethodGet, "/v1/queues", nil, owner())
	if w.Code != 200 {
		t.Fatalf("queues: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Data []QueueView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Data) != 4 {
		t.Fatalf("list: %+v %v", listed, err)
	}

	paused := true
	w = h.do(http.MethodPatch, "/v1/queues/"+bus.StreamSubmit, map[string]any{"paused": paused}, owner())
	if w.Code != 200 {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}
	if !h.b.QueuePaused(bus.StreamSubmit) {
		t.Fatal("bus not paused")
	}

	var calls int
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	stop, err := h.b.ConsumePartition(ctx, bus.StreamSubmit, "cluster-pause", bus.SubmitSubject("*"), 1, func(context.Context, bus.Message) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := h.b.Publish(h.ctx, bus.SubmitSubject("acc_x"), []byte(`{}`), "q-pause-1", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("consumed while paused: %d", calls)
	}

	paused = false
	w = h.do(http.MethodPatch, "/v1/queues/"+bus.StreamSubmit, map[string]any{"paused": paused, "keep": "3d"}, owner())
	if w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && calls == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if calls == 0 {
		t.Fatal("did not resume")
	}

	w = h.do(http.MethodPatch, "/v1/queues/NOPE", map[string]any{"paused": true}, owner())
	if w.Code != 404 {
		t.Fatalf("unknown queue: %d", w.Code)
	}
	if errs.CodeOf(h.svc.Drain(h.ctx, "ghost")) != errs.CodeNotFound {
		t.Fatal("drain unknown")
	}
}

func TestIntegrationModuleBasics(t *testing.T) {
	h := newHarness(t)
	m := &Module{}
	if m.Name() != "cluster" || len(m.Roles()) != 1 {
		t.Fatal(m.Name(), m.Roles())
	}
	if h.svc == nil {
		t.Fatal("service")
	}
}

func TestIntegrationLoadPausedQueuesAndKeepValidation(t *testing.T) {
	h := newHarness(t)
	_, err := h.db.Collection("settings").InsertOne(h.ctx, bson.M{
		"_id": QueuesDocID,
		"queues": bson.M{
			bus.StreamSysmail: bson.M{"paused": true, "keep": "1d"},
		},
		"version": int64(1), "updatedAt": time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.loadQueueSettings(h.ctx); err != nil {
		t.Fatal(err)
	}
	if !h.b.QueuePaused(bus.StreamSysmail) {
		t.Fatal("sysmail should be paused from settings")
	}

	bad := "nope"
	if _, err := h.svc.PatchQueue(h.ctx, bus.StreamSysmail, nil, &bad); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad keep: %v", err)
	}
	empty := ""
	if _, err := h.svc.PatchQueue(h.ctx, bus.StreamSysmail, nil, &empty); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty keep: %v", err)
	}
	w := h.do(http.MethodPatch, "/v1/queues/"+bus.StreamSysmail, map[string]any{}, owner())
	if w.Code != 422 && w.Code != 400 {
		t.Fatalf("empty patch: %d %s", w.Code, w.Body.String())
	}

	// stats without organisation
	cctx := tenancy.WithCaller(h.ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", Role: tenancy.RoleViewer, InstanceOwner: true})
	if _, err := h.svc.Stats(cctx); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("stats no org: %v", err)
	}
	w = h.do(http.MethodGet, "/v1/stats", nil, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", Role: tenancy.RoleViewer})
	if w.Code != 403 {
		t.Fatalf("stats http no org: %d", w.Code)
	}

	// unknown state bucket still counted
	org := store.NewID(store.PrefixOrganization)
	_, _ = h.db.Collection("accounts").InsertOne(h.ctx, bson.M{"_id": "acc_x", "organizationId": org})
	st, err := h.svc.Stats(tenancy.WithCaller(h.ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleViewer,
	}))
	if err != nil || st.Accounts["unknown"] != 1 {
		t.Fatalf("unknown state: %+v %v", st, err)
	}

	keep := "12h"
	if _, err := h.svc.PatchQueue(h.ctx, bus.StreamEvents, nil, &keep); err != nil {
		t.Fatal(err)
	}
	qs, err := h.svc.Queues(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range qs {
		if q.Name == bus.StreamEvents && q.Keep == "12h" {
			found = true
		}
		if q.Name == bus.StreamSysmail && !q.Paused {
			t.Fatal("sysmail should still be paused from settings doc")
		}
	}
	if !found {
		t.Fatalf("keep not reflected: %+v", qs)
	}
}

func TestIntegrationHandlersDirect(t *testing.T) {
	h := newHarness(t)
	m := &Module{svc: h.svc}
	if _, err := m.getCluster(context.Background(), &struct{}{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("unauth cluster: %v", err)
	}
	if _, err := m.getStats(context.Background(), &statsIn{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("unauth stats: %v", err)
	}
	if _, err := m.listQueues(context.Background(), &listQueuesIn{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("unauth queues: %v", err)
	}
	if _, err := m.patchQueue(context.Background(), &patchQueueIn{Queue: bus.StreamSubmit}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("unauth patch: %v", err)
	}
	if _, err := m.drainNode(context.Background(), &drainIn{NodeID: "x"}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("unauth drain: %v", err)
	}

	emptyOrg := store.NewID(store.PrefixOrganization)
	st, err := h.svc.Stats(tenancy.WithCaller(h.ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: emptyOrg, Role: tenancy.RoleViewer,
	}))
	if err != nil || st.EventsLast24h != 0 || st.DeadLetters != 0 {
		t.Fatalf("empty org stats: %+v %v", st, err)
	}

	// Start without audit writer still works
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "api-2", Log: quiet})
	app.Provide(a, h.db)
	app.Provide(a, h.b)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m2 := &Module{Log: quiet}
	if err := m2.Start(h.ctx, a); err != nil {
		t.Fatal(err)
	}
	_ = m2.Stop(h.ctx)
	if err := Migration(99).Up(h.ctx, h.db); err != nil {
		t.Fatal(err)
	}

	a3 := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "api-3", Log: quiet})
	if err := (&Module{}).Start(h.ctx, a3); err == nil {
		t.Fatal("Start without deps must fail")
	}
	app.Provide(a3, h.db)
	if err := (&Module{}).Start(h.ctx, a3); err == nil {
		t.Fatal("Start without bus must fail")
	}
	app.Provide(a3, h.b)
	if err := (&Module{}).Start(h.ctx, a3); err == nil {
		t.Fatal("Start without http server must fail")
	}

	_, _ = h.db.Collection("settings").ReplaceOne(h.ctx, bson.M{"_id": QueuesDocID}, bson.M{
		"_id": QueuesDocID, "queues": bson.M{bus.StreamDelivery: bson.M{"paused": false}}, "version": int64(2),
	})
	if err := h.svc.loadQueueSettings(h.ctx); err != nil {
		t.Fatal(err)
	}
	if h.b.QueuePaused(bus.StreamDelivery) {
		t.Fatal("delivery should be unpaused")
	}
	_, _ = h.db.Collection("settings").ReplaceOne(h.ctx, bson.M{"_id": QueuesDocID}, bson.M{
		"_id": QueuesDocID, "version": int64(3),
	})
	doc, err := h.svc.readQueueDoc(h.ctx)
	if err != nil || doc.Queues == nil {
		t.Fatalf("nil queues map: %+v %v", doc, err)
	}
	noAudit := &Service{DB: h.db, Bus: h.b}
	p := true
	if _, err := noAudit.PatchQueue(h.ctx, bus.StreamDelivery, &p, nil); err != nil {
		t.Fatal(err)
	}
}
