//go:build integration

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (h *harness) kpis(period string, c tenancy.Caller) *KPIs {
	h.t.Helper()
	path := "/v1/stats"
	if period != "" {
		path += "?period=" + period
	}
	w := h.do(http.MethodGet, path, nil, c)
	if w.Code != 200 {
		h.t.Fatalf("stats %s: %d %s", period, w.Code, w.Body.String())
	}
	var v StatsView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		h.t.Fatal(err)
	}
	if v.KPIs == nil {
		h.t.Fatal("kpis missing")
	}
	return v.KPIs
}

func sum(b []int64) int64 {
	var n int64
	for _, v := range b {
		n += v
	}
	return n
}

func prevOf(t *testing.T, name string, k KPI) int64 {
	t.Helper()
	if k.Previous == nil {
		t.Fatalf("%s: previous is null", name)
	}
	return *k.Previous
}

func (h *harness) insert(coll string, docs ...bson.M) {
	h.t.Helper()
	in := make([]any, len(docs))
	for i, d := range docs {
		in[i] = d
	}
	if _, err := h.db.Collection(coll).InsertMany(h.ctx, in); err != nil {
		h.t.Fatal(err)
	}
}

// TestIntegrationKPIsEndToEnd seeds events, sends, deliveries and real API calls (through the middleware)
// for two organisations in the current and previous period and checks counts, trends, gating, cache and
// isolation (W74 §1–§2).
func TestIntegrationKPIsEndToEnd(t *testing.T) {
	h := newHarness(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	id := 0
	next := func(p string) string { id++; return fmt.Sprintf("%s_%d", p, id) }

	ev := func(org, typ string, at time.Time) bson.M {
		return bson.M{"_id": next("evt"), "organizationId": org, "type": typ, "time": at}
	}
	h.insert("events",
		ev(orgA, "message.received", ago(90*time.Minute)), ev(orgA, "message.received", ago(2*time.Hour)),
		ev(orgA, "message.received", ago(23*time.Hour)),
		ev(orgA, "message.received", ago(30*time.Hour)), ev(orgA, "message.received", ago(47*time.Hour)),
		ev(orgA, "message.received", ago(50*time.Hour)), // outside both periods
		ev(orgA, "message.sent", ago(time.Hour)),        // other type
		ev(orgB, "message.received", ago(time.Hour)), ev(orgB, "message.received", ago(time.Hour)),
	)
	out := func(org, state string, created time.Time, sent *time.Time) bson.M {
		d := bson.M{"_id": next("out"), "organizationId": org, "state": state, "createdAt": created}
		if sent != nil {
			d["sentAt"] = *sent
		}
		return d
	}
	s1, s2, s3, sb := ago(2*time.Hour), ago(3*time.Hour), ago(26*time.Hour), ago(time.Hour)
	h.insert("outboundMessages",
		out(orgA, "sent", ago(3*time.Hour), &s1), out(orgA, "sent", ago(4*time.Hour), &s2),
		out(orgA, "sent", ago(27*time.Hour), &s3),
		out(orgA, "failed", ago(3*time.Hour), nil), out(orgA, "queued", ago(time.Hour), nil),
		out(orgB, "sent", ago(2*time.Hour), &sb),
	)
	dl := func(org, state string, at time.Time) bson.M {
		return bson.M{"_id": next("dlv"), "organizationId": org, "state": state, "createdAt": at}
	}
	h.insert("deliveries",
		dl(orgA, "succeeded", ago(time.Hour)), dl(orgA, "succeeded", ago(time.Hour)), dl(orgA, "succeeded", ago(5*time.Hour)),
		dl(orgA, "failed", ago(2*time.Hour)), dl(orgA, "deadLettered", ago(2*time.Hour)),
		dl(orgA, "deadLettered", ago(40*time.Hour)), dl(orgA, "deadLettered", ago(41*time.Hour)),
		dl(orgA, "pending", ago(time.Hour)),
		dl(orgB, "failed", ago(time.Hour)), dl(orgB, "failed", ago(time.Hour)), dl(orgB, "failed", ago(time.Hour)),
	)
	h.insert("accounts",
		bson.M{"_id": next("acc"), "organizationId": orgA, "state": "connected"},
		bson.M{"_id": next("acc"), "organizationId": orgA, "state": "connected"},
		bson.M{"_id": next("acc"), "organizationId": orgA, "state": "authError"},
		bson.M{"_id": next("acc"), "organizationId": orgB, "state": "connected"},
	)

	ownerA := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_o", OrganizationID: orgA, Role: tenancy.RoleOwner, InstanceOwner: true}
	adminA := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_a", OrganizationID: orgA, Role: tenancy.RoleAdmin}
	viewerA := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_v", OrganizationID: orgA, Role: tenancy.RoleViewer}
	adminB := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_b", OrganizationID: orgB, Role: tenancy.RoleAdmin}
	ownerB := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_ob", OrganizationID: orgB, Role: tenancy.RoleOwner, InstanceOwner: true}

	// Real API calls through the counter middleware: 2 successes + 1 client error for A, 1 success for B.
	for _, c := range []tenancy.Caller{ownerA, ownerA, ownerB} {
		if w := h.do(http.MethodGet, "/v1/cluster", nil, c); w.Code != 200 {
			t.Fatalf("cluster: %d", w.Code)
		}
	}
	if w := h.do(http.MethodGet, "/v1/cluster", nil, adminA); w.Code != 403 {
		t.Fatalf("admin cluster: %d", w.Code)
	}
	// A failed flush keeps the counts for the next one.
	cctx, cancel := context.WithCancel(h.ctx)
	cancel()
	if err := h.mod.Counter().Flush(cctx); err == nil {
		t.Fatal("flush with cancelled context must fail")
	}
	if err := h.mod.Counter().Flush(h.ctx); err != nil {
		t.Fatal(err)
	}
	if okA, ceA := h.apiTotals(orgA); okA != 2 || ceA != 1 {
		t.Fatalf("api docs A: %d/%d", okA, ceA)
	}

	k := h.kpis("24h", adminA)
	if k.Period != "24h" || k.BucketSeconds != 3600 || len(k.MessagesReceived.Buckets) != 24 {
		t.Fatalf("window: %+v", k)
	}
	check := func(name string, got KPI, cur, prev int64) {
		t.Helper()
		if got.Current != cur || prevOf(t, name, got) != prev || sum(got.Buckets) != cur {
			t.Fatalf("%s: %+v want %d/%d", name, got, cur, prev)
		}
	}
	check("received", k.MessagesReceived, 3, 2)
	check("sent", k.MessagesSent, 2, 1)
	check("failed", k.MessagesFailed, 1, 0)
	check("delivered", k.WebhooksDelivered, 3, 0)
	check("webhooksFailed", k.WebhooksFailed, 2, 2)
	if k.Accounts != (AccountKPIs{Total: 3, Connected: 2, Attention: 1}) {
		t.Fatalf("accounts: %+v", k.Accounts)
	}
	if k.APICalls == nil {
		t.Fatal("admin must see api calls")
	}
	check("apiOK", k.APICalls.Succeeded, 2, 0)
	check("apiFailed", k.APICalls.Failed, 1, 0)
	if k.APICalls.ClientErrors != 1 || k.APICalls.ServerErrors != 0 || k.APICalls.LatencyP95Ms == nil {
		t.Fatalf("api split: %+v", k.APICalls)
	}
	if k.Queues != nil || k.Infra != nil {
		t.Fatal("admin must not see infra")
	}

	// Viewer reads the cached entry the admin populated: api calls stripped.
	if v := h.kpis("24h", viewerA); v.APICalls != nil || v.Queues != nil || v.MessagesReceived.Current != 3 {
		t.Fatalf("viewer: %+v", v)
	}
	// API keys: logs:read unlocks api calls, accounts:read alone does not; keys never see infra.
	logsKey := tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_logs", OrganizationID: orgA,
		Scopes: []string{"accounts:read", "logs:read"}}
	if k := h.kpis("24h", logsKey); k.APICalls == nil || k.APICalls.Succeeded.Current != 2 || k.Queues != nil || k.Infra != nil {
		t.Fatalf("logs:read key: %+v", k.APICalls)
	}
	plainKey := tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_plain", OrganizationID: orgA,
		Scopes: []string{"accounts:read"}}
	if k := h.kpis("24h", plainKey); k.APICalls != nil || k.Queues != nil {
		t.Fatalf("accounts:read key: %+v", k.APICalls)
	}
	// Instance owner: queues + infra, measured live.
	o := h.kpis("24h", ownerA)
	if len(o.Queues) != 4 || o.Infra == nil || o.Infra.MongoError != "" || o.Infra.BusError != "" || o.APICalls == nil {
		t.Fatalf("owner: %+v %+v", o.Queues, o.Infra)
	}
	if o.Queues[0].Key != "webhooks" || o.Queues[1].Key != "submission" || o.Queues[2].Key != "sysmail" {
		t.Fatalf("queue keys: %+v", o.Queues)
	}

	// Organisation B sees only its own data.
	b := h.kpis("24h", adminB)
	if b.MessagesReceived.Current != 2 || b.MessagesSent.Current != 1 || b.WebhooksFailed.Current != 3 ||
		b.WebhooksDelivered.Current != 0 || b.Accounts.Total != 1 || b.APICalls.Succeeded.Current != 1 ||
		b.APICalls.Failed.Current != 0 {
		t.Fatalf("org B: %+v api=%+v", b, b.APICalls)
	}

	// Cache: a new event is invisible for 15 s, visible after the entry is dropped.
	h.insert("events", ev(orgA, "message.received", ago(time.Minute)))
	if c := h.kpis("24h", adminA); c.MessagesReceived.Current != 3 {
		t.Fatalf("cache bypassed: %d", c.MessagesReceived.Current)
	}
	if err := h.b.CacheDelete(h.ctx, kpiCacheKey(orgA, "24h", window{})); err != nil {
		t.Fatal(err)
	}
	if c := h.kpis("", adminA); c.MessagesReceived.Current != 4 || c.Period != "24h" {
		t.Fatalf("after cache drop: %+v", c.MessagesReceived)
	}

	// 1 h: minute resolution of api calls; 7 d / 30 d: retention gating.
	if one := h.kpis("1h", adminA); one.APICalls.Succeeded.Current != 2 || len(one.MessagesReceived.Buckets) != 12 ||
		one.MessagesReceived.Current != 1 {
		t.Fatalf("1h: %+v %+v", one.APICalls.Succeeded, one.MessagesReceived)
	}
	seven := h.kpis("7d", adminA)
	if seven.MessagesReceived.Previous != nil || seven.MessagesReceived.Partial || seven.WebhooksDelivered.Previous == nil ||
		seven.MessagesReceived.Current != 7 || seven.APICalls.Succeeded.Current != 2 || len(seven.WebhooksFailed.Buckets) != 28 {
		t.Fatalf("7d: %+v", seven)
	}
	thirty := h.kpis("30d", adminA)
	if !thirty.MessagesReceived.Partial || thirty.WebhooksDelivered.Previous != nil || thirty.MessagesSent.Previous == nil {
		t.Fatalf("30d: %+v", thirty)
	}

	if w := h.do(http.MethodGet, "/v1/stats?period=2h", nil, adminA); w.Code != 422 {
		t.Fatalf("bad period: %d %s", w.Code, w.Body.String())
	}
	// The stats endpoint itself is not counted.
	if err := h.mod.Counter().Flush(h.ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.b.CacheDelete(h.ctx, kpiCacheKey(orgA, "24h", window{})); err != nil {
		t.Fatal(err)
	}
	if again := h.kpis("24h", adminA); again.APICalls.Succeeded.Current != 2 {
		t.Fatalf("stats calls counted: %d", again.APICalls.Succeeded.Current)
	}

	// Stop flushes what is still in memory.
	h.do(http.MethodGet, "/v1/cluster", nil, ownerB)
	if err := h.mod.Stop(h.ctx); err != nil {
		t.Fatal(err)
	}
	if okB, _ := h.apiTotals(orgB); okB != 2 {
		t.Fatalf("stop flush: %d", okB)
	}
}

// apiTotals sums the stored hourly buckets of org (and checks every bucket carries its TTL field).
func (h *harness) apiTotals(org string) (success, clientError int64) {
	h.t.Helper()
	cur, err := h.db.Collection(APICallsCollection).Find(h.ctx, bson.M{"organizationId": org})
	if err != nil {
		h.t.Fatal(err)
	}
	var docs []bson.M
	if err := cur.All(h.ctx, &docs); err != nil {
		h.t.Fatal(err)
	}
	for _, d := range docs {
		if d["expireAt"] == nil || d["hour"] == nil {
			h.t.Fatalf("bucket without ttl/hour: %+v", d)
		}
		success += d["success"].(int64)
		clientError += d["clientError"].(int64)
	}
	return success, clientError
}

func TestIntegrationKPIsRetentionOverrideAndNoBus(t *testing.T) {
	h := newHarness(t)
	org := store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	h.insert("events",
		bson.M{"_id": "evt_r1", "organizationId": org, "type": "message.received", "time": now.Add(-time.Hour)},
		bson.M{"_id": "evt_r2", "organizationId": org, "type": "message.received", "time": now.Add(-8 * 24 * time.Hour)},
	)
	s := &Service{DB: h.db, Retention: func() Retention { return Retention{Events: 30 * 24 * time.Hour} }}
	ctx := tenancy.WithCaller(h.ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org,
		Role: tenancy.RoleOwner, InstanceOwner: true})
	k, err := s.KPIs(ctx, KPIRequest{Period: "7d"})
	if err != nil {
		t.Fatal(err)
	}
	if k.MessagesReceived.Current != 1 || k.MessagesReceived.Previous == nil || *k.MessagesReceived.Previous != 1 {
		t.Fatalf("override: %+v", k.MessagesReceived)
	}
	if k.Queues != nil || k.Infra == nil || k.Infra.BusError != "unreachable" {
		t.Fatalf("no bus: %+v", k.Infra)
	}
}

// TestIntegrationKPIsPerf100k measures the uncached KPI computation on 100k events (W74 §1.3) and checks
// the events aggregation uses the migration-47 index.
func TestIntegrationKPIsPerf100k(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	h := newHarness(t)
	org := store.NewID(store.PrefixOrganization)
	other := store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	const total = 100_000
	batch := make([]any, 0, 5000)
	seedStart := time.Now()
	for i := range total {
		o := org
		if i%4 == 0 {
			o = other
		}
		typ := "message.received"
		if i%3 == 0 {
			typ = "message.updated"
		}
		batch = append(batch, bson.M{
			"_id": fmt.Sprintf("evt_p%06d", i), "organizationId": o, "type": typ,
			"time": now.Add(-time.Duration(i%(60*24*30)) * time.Minute),
		})
		if len(batch) == cap(batch) {
			if _, err := h.db.Collection("events").InsertMany(h.ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	t.Logf("seeded %d events in %s", total, time.Since(seedStart).Round(time.Millisecond))
	for _, p := range []string{"1h", "24h", "7d", "30d"} {
		start := time.Now()
		k, err := h.svc.orgKPIs(h.ctx, org, p, newWindow(periods[p], time.Now()), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("period %-3s uncached orgKPIs %s (messagesReceived=%d)", p, time.Since(start).Round(time.Microsecond), k.MessagesReceived.Current)
	}
	var explain bson.M
	w := newWindow(periods["30d"], now)
	err := h.db.DB.RunCommand(h.ctx, bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "aggregate", Value: "events"},
			{Key: "pipeline", Value: bson.A{bson.M{"$match": bson.M{
				"organizationId": org, "type": "message.received",
				"time": bson.M{"$gte": w.prevStart, "$lt": w.to},
			}}}},
			{Key: "cursor", Value: bson.M{}},
		}},
		{Key: "verbosity", Value: "queryPlanner"},
	}).Decode(&explain)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(explain)
	if !strings.Contains(string(raw), "IXSCAN") || strings.Contains(string(raw), "COLLSCAN") {
		t.Fatalf("events aggregation is not an index scan: %s", raw)
	}
	if !strings.Contains(string(raw), "org_type_time") {
		t.Logf("winning index is not org_type_time: %s", raw)
	}
}
