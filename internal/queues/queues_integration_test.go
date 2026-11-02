//go:build integration

package queues

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
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

type fakeHub struct {
	mu   sync.Mutex
	msgs []struct {
		org string
		c   Change
	}
}

func (f *fakeHub) Publish(org, topic, typ string, data any, _ map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if topic != topicUpdates || typ != typeChanged {
		return fmt.Errorf("unexpected topic %s/%s", topic, typ)
	}
	f.msgs = append(f.msgs, struct {
		org string
		c   Change
	}{org, data.(Change)})
	return nil
}

func (f *fakeHub) orgs() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, m := range f.msgs {
		out[m.org]++
	}
	return out
}

type rep struct {
	done, total int64
	cancel      bool
}

func (r *rep) Progress(_ context.Context, d, t int64) { r.done, r.total = d, t }
func (r *rep) Cancelled() bool                        { return r.cancel }

type harness struct {
	t    *testing.T
	ctx  context.Context
	db   *store.DB
	srv  *httpapi.Server
	svc  *Service
	jobs *jobs.Service
	hub  *fakeHub
}

const (
	orgA = "org_01m5caqpk0kast63yc13wpy2d9"
	orgB = "org_01m5x8b3d0n2c6t4r9y1k7w5qe"
	accA = "acc_01m5cvnpb031g57sqct6mzhq94"
	accX = "acc_01m5x5dtxq189hak49x3b25a5h" // second account of org A
	accB = "acc_01m5f9z8y0x7w6v5u4t3s2r1qp"
	epA  = "whe_01m5d9x2r0f4y8j6k3c1v7q2pn"
	epB  = "whe_01m5d9y3s0g5z9k7l4d2w8r3qo"
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_queues_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, m := range []store.Migration{Migration(1), jobs.Migration(2), audit.Migration(3)} {
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "api-1", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	app.Provide(a, &audit.Writer{DB: db, Log: quiet})
	js := &jobs.Service{DB: db, Log: quiet}
	app.Provide(a, js)
	m := &Module{Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	hub := &fakeHub{}
	m.Service().Hub = hub
	if got, ok := app.Get[*Service](a); !ok || got != m.Service() {
		t.Fatal("service not provided")
	}
	return &harness{t: t, ctx: ctx, db: db, srv: srv, svc: m.Service(), jobs: js, hub: hub}
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

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (%s)", v, err, w.Body.String())
	}
	return v
}

func admin(org string) tenancy.Caller {
	return tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_01m5ccvej0dqefkn0xhkcq2q6z", OrganizationID: org, Role: tenancy.RoleAdmin}
}

func viewer(org string) tenancy.Caller {
	return tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_viewer", OrganizationID: org, Role: tenancy.RoleViewer}
}

func instanceOwner() tenancy.Caller {
	return tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_owner", OrganizationID: orgA, Role: tenancy.RoleOwner, InstanceOwner: true}
}

var seq int

func (h *harness) outbound(org, acc, state string, attempts int, system bool) string {
	h.t.Helper()
	seq++
	id := store.NewID(store.PrefixOutbound)
	att := bson.A{}
	for i := 0; i < attempts; i++ {
		att = append(att, bson.M{"at": time.Now().UTC(), "stage": "done", "code": 451})
	}
	doc := bson.M{
		"_id": id, "organizationId": org, "accountId": acc, "state": state, "policy": "standard",
		"envelope":  bson.M{"from": "sender@example.com", "to": bson.A{fmt.Sprintf("rcpt%d@example.com", seq)}, "cc": bson.A{"cc@example.org"}},
		"subject":   fmt.Sprintf("Invoice %d", seq),
		"attempts":  att,
		"createdAt": time.Now().UTC(), "version": 1, "maxAttempts": 5,
		"copyToSent": bson.M{"enabled": false, "state": "skipped"},
	}
	if state == submit.StateQueued && attempts > 0 {
		doc["nextAttemptAt"] = time.Now().UTC().Add(10 * time.Minute)
	}
	if system {
		doc["system"] = true
	}
	if _, err := h.db.Collection(submit.Collection).InsertOne(h.ctx, doc); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) delivery(org, acc, ep, state string) string {
	h.t.Helper()
	id := store.NewID(store.PrefixDelivery)
	if _, err := h.db.Collection(webhook.DeliveriesCollection).InsertOne(h.ctx, bson.M{
		"_id": id, "organizationId": org, "accountId": acc, "endpointId": ep, "eventId": store.NewID(store.PrefixEvent),
		"eventType": "message.received", "state": state, "attempts": bson.A{}, "isReplay": false,
		"createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(), "expireAt": time.Now().Add(time.Hour), "version": 1,
	}); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) sysmailRecord(state string, attempts int) string {
	h.t.Helper()
	id := store.NewID(store.PrefixJob)
	now := time.Now().UTC()
	if _, err := h.db.Collection(sysmail.RecordsCollection).InsertOne(h.ctx, sysmail.Record{
		ID: id, Kind: sysmail.KindInvitation, To: "new.member@example.com", State: state, Attempts: attempts,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(time.Hour),
	}); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) stateOf(coll, id string) string {
	h.t.Helper()
	var doc struct {
		State string `bson:"state"`
	}
	if err := h.db.Collection(coll).FindOne(h.ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
		h.t.Fatal(err)
	}
	return doc.State
}

func (h *harness) runJob(c tenancy.Caller, jobID string) JobResult {
	h.t.Helper()
	j, err := h.jobs.Get(tenancy.WithCaller(h.ctx, c), jobID)
	if err != nil {
		h.t.Fatalf("job get: %v", err)
	}
	if j.Kind != jobKindCancel {
		h.t.Fatalf("kind %s", j.Kind)
	}
	r := &rep{}
	out, err := h.svc.HandleCancelJob(h.ctx, j, r)
	if err != nil {
		h.t.Fatalf("job: %v", err)
	}
	res := out.(JobResult)
	if r.done != res.Cancelled+res.Skipped {
		h.t.Fatalf("progress %d/%d vs %+v", r.done, r.total, res)
	}
	return res
}

func TestIntegrationListSendingScopedAndMapped(t *testing.T) {
	h := newHarness(t)
	sched := h.outbound(orgA, accA, submit.StateScheduled, 0, false)
	queued := h.outbound(orgA, accA, submit.StateQueued, 0, false)
	retry := h.outbound(orgA, accX, submit.StateQueued, 2, false)
	sending := h.outbound(orgA, accA, submit.StateSending, 1, false)
	h.outbound(orgA, accA, submit.StateSent, 1, false)            // final: not listed
	h.outbound(orgA, accA, submit.StateQueued, 0, true)           // system mail via account: not in org scope
	other := h.outbound(orgB, accB, submit.StateQueued, 0, false) // other organisation

	w := h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items", nil, viewer(orgA))
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	p := decode[Page](t, w)
	if p.Total != 4 || len(p.Data) != 4 || p.Page != 1 || p.Limit != defListLimit {
		t.Fatalf("page: %+v", p)
	}
	byID := map[string]Item{}
	for _, it := range p.Data {
		byID[it.ID] = it
		if it.ID == other {
			t.Fatal("other organisation listed")
		}
	}
	if byID[sched].State != "scheduled" || byID[queued].State != "queued" || byID[retry].State != "retrying" {
		t.Fatalf("states: %+v", byID)
	}
	if byID[retry].Attempts != 2 || byID[retry].NextAttemptAt == nil || byID[retry].Recipient == "" || byID[retry].RecipientCount != 2 {
		t.Fatalf("retry item: %+v", byID[retry])
	}
	if s := byID[sending]; s.Cancellable || s.NotCancellableReason != ReasonInFlight || s.State != "sending" {
		t.Fatalf("sending item: %+v", s)
	}
	if p.Data[0].ID != sched { // oldest first
		t.Fatalf("order: %s", p.Data[0].ID)
	}

	// Filters, search, sort, paging.
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?state=retrying", nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total != 1 || p.Data[0].ID != retry {
		t.Fatalf("state filter: %+v", p)
	}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?accountId="+accX, nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total != 1 {
		t.Fatalf("account filter: %+v", p)
	}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?q="+byID[queued].Recipient[:6], nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total < 1 {
		t.Fatalf("search: %+v", p)
	}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?q=INVOICE", nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total != 4 {
		t.Fatalf("subject search (case-insensitive prefix): %+v", p)
	}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?q=.*", nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total != 0 {
		t.Fatalf("regex must be quoted: %+v", p)
	}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?sort=-createdAt&limit=2&page=2", nil, viewer(orgA))
	if p := decode[Page](t, w); len(p.Data) != 2 || p.Data[1].ID != sched || p.Total != 4 {
		t.Fatalf("sort/page: %+v", p)
	}
	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?createdFrom="+from+"&sort=nextAttemptAt", nil, viewer(orgA))
	if p := decode[Page](t, w); p.Total != 4 {
		t.Fatalf("date filter: %d %+v", w.Code, p)
	}

	// Instance owner, every organisation: includes org B and system mail sent through an account.
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?scope=all", nil, instanceOwner())
	if p := decode[Page](t, w); p.Total != 6 {
		t.Fatalf("all scope: %+v", p)
	}
	if w := h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?scope=all", nil, admin(orgA)); w.Code != 403 {
		t.Fatalf("admin scope=all: %d", w.Code)
	}
}

func TestIntegrationListErrors(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		path string
		c    tenancy.Caller
		code int
	}{
		{"/v1/queues/EMAILBRIDGE_EVENTS/items", admin(orgA), 404},
		{"/v1/queues/NOPE/items", admin(orgA), 404},
		{"/v1/queues/EMAILBRIDGE_SUBMIT/items?state=deadLettered", admin(orgA), 422},
		{"/v1/queues/EMAILBRIDGE_SUBMIT/items?endpointId=" + epA, admin(orgA), 422},
		{"/v1/queues/EMAILBRIDGE_SYSMAIL/items?accountId=" + accA, instanceOwner(), 422},
		{"/v1/queues/EMAILBRIDGE_SUBMIT/items?sort=subject", admin(orgA), 422},
		{"/v1/queues/EMAILBRIDGE_SUBMIT/items?createdFrom=2026-10-27T12:00:00Z&createdTo=2026-10-27T10:00:00Z", admin(orgA), 422},
		{"/v1/queues/EMAILBRIDGE_SYSMAIL/items", admin(orgA), 403},
		{"/v1/queues/EMAILBRIDGE_SUBMIT/items", tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: orgA, Scopes: []string{"deliveries:read"}}, 403},
	}
	for _, c := range cases {
		if w := h.do(http.MethodGet, c.path, nil, c.c); w.Code != c.code {
			t.Errorf("%s: %d want %d (%s)", c.path, w.Code, c.code, w.Body.String())
		}
	}
	if _, err := h.svc.List(context.Background(), QueueSending, ListQuery{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("no caller: %v", err)
	}
	if _, err := h.svc.List(tenancy.WithCaller(h.ctx, admin(orgA)), QueueSending, ListQuery{Filter: Filter{Q: strings.Repeat("a", 201)}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}
}

func TestIntegrationCancelIDsCrossTenant(t *testing.T) {
	h := newHarness(t)
	mine := h.outbound(orgA, accA, submit.StateQueued, 0, false)
	mine2 := h.outbound(orgA, accA, submit.StateScheduled, 0, false)
	sending := h.outbound(orgA, accA, submit.StateSending, 1, false)
	sent := h.outbound(orgA, accA, submit.StateSent, 1, false)
	theirs := h.outbound(orgB, accB, submit.StateQueued, 0, false)

	// Viewers cannot cancel.
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{mine}}, viewer(orgA)); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	// Single id: cancelled.
	w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{mine}}, admin(orgA))
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if r := decode[CancelResult](t, w); r.Cancelled == nil || *r.Cancelled != 1 || r.Job != nil {
		t.Fatalf("result: %+v", r)
	}
	if st := h.stateOf(submit.Collection, mine); st != submit.StateCancelled {
		t.Fatalf("state %s", st)
	}
	// Single id problems: in flight 409, final 409, another organisation 404.
	for id, want := range map[string]int{sending: 409, sent: 409, theirs: 404, mine: 409} {
		w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{id}}, admin(orgA))
		if w.Code != want {
			t.Fatalf("%s: %d want %d %s", id, w.Code, want, w.Body.String())
		}
		if want == 409 && !strings.Contains(w.Body.String(), string(errs.CodeQueueItemNotCancellable)) {
			t.Fatalf("code: %s", w.Body.String())
		}
	}
	if st := h.stateOf(submit.Collection, theirs); st != submit.StateQueued {
		t.Fatalf("other organisation touched: %s", st)
	}
	// Several ids: per-id reasons.
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"ids": []string{mine2, sending, sent, theirs, mine2}}, admin(orgA))
	r := decode[CancelResult](t, w)
	if w.Code != 200 || *r.Cancelled != 1 || len(r.Skipped) != 3 {
		t.Fatalf("multi: %d %+v", w.Code, r)
	}
	reasons := map[string]string{}
	for _, s := range r.Skipped {
		reasons[s.ID] = s.Reason
	}
	if reasons[sending] != ReasonInFlight || reasons[sent] != ReasonFinal || reasons[theirs] != ReasonNotFound {
		t.Fatalf("reasons: %+v", reasons)
	}
	// Malformed / foreign-queue ids.
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{"dlv_01m5f3a7c0e9r4t2y6u8i1o3pk"}}, admin(orgA)); w.Code != 422 {
		t.Fatalf("foreign id: %d", w.Code)
	}
	// The instance owner reaches another organisation's id only with scope=all.
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{theirs}, "scope": "all"}, instanceOwner())
	if w.Code != 200 || h.stateOf(submit.Collection, theirs) != submit.StateCancelled {
		t.Fatalf("owner all: %d %s", w.Code, w.Body.String())
	}
	if n, _ := h.db.Collection("auditLog").CountDocuments(h.ctx, bson.M{"action": "queue.items.cancelled"}); n < 3 {
		t.Fatalf("audit entries: %d", n)
	}
	if got := h.hub.orgs(); got[orgA] == 0 || got[orgB] == 0 {
		t.Fatalf("realtime: %+v", got)
	}
}

func TestIntegrationCancelByFilterJob(t *testing.T) {
	h := newHarness(t)
	var target []string
	for i := 0; i < 3; i++ {
		target = append(target, h.outbound(orgA, accA, submit.StateQueued, 1, false)) // retrying
	}
	keep := h.outbound(orgA, accA, submit.StateQueued, 0, false) // queued, not retrying
	keepX := h.outbound(orgA, accX, submit.StateQueued, 2, false)
	theirs := h.outbound(orgB, accB, submit.StateQueued, 1, false)

	// Validation.
	for _, body := range []map[string]any{
		{},
		{"ids": []string{target[0]}, "all": true},
		{"filter": map[string]any{}},
		{"filter": map[string]any{"state": "delivering"}},
		{"filter": map[string]any{"state": "queued"}, "scope": "everyone"},
	} {
		if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", body, admin(orgA)); w.Code != 422 {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
	}
	// Nothing matches: no job.
	w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"filter": map[string]any{"state": "scheduled"}}, admin(orgA))
	if r := decode[CancelResult](t, w); w.Code != 200 || r.Job != nil || r.Matched == nil || *r.Matched != 0 {
		t.Fatalf("empty match: %d %+v", w.Code, r)
	}

	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"filter": map[string]any{"state": "retrying", "accountId": accA}}, admin(orgA))
	if w.Code != 202 {
		t.Fatalf("filter: %d %s", w.Code, w.Body.String())
	}
	r := decode[CancelResult](t, w)
	if r.Job == nil || r.Matched == nil || *r.Matched != 3 || r.Job.OrganizationID != orgA {
		t.Fatalf("job result: %+v", r)
	}
	late := h.outbound(orgA, accA, submit.StateQueued, 1, false) // queued after the request: never touched
	res := h.runJob(admin(orgA), r.Job.ID)
	if res.Matched != 3 || res.Cancelled != 3 || res.Skipped != 0 {
		t.Fatalf("job: %+v", res)
	}
	for _, id := range target {
		if st := h.stateOf(submit.Collection, id); st != submit.StateCancelled {
			t.Fatalf("%s: %s", id, st)
		}
	}
	for _, id := range []string{keep, keepX, theirs, late} {
		if st := h.stateOf(submit.Collection, id); st != submit.StateQueued {
			t.Fatalf("%s touched: %s", id, st)
		}
	}
	// Idempotent re-run.
	if res := h.runJob(admin(orgA), r.Job.ID); res.Cancelled != 0 {
		t.Fatalf("re-run: %+v", res)
	}
	if n, _ := h.db.Collection("auditLog").CountDocuments(h.ctx, bson.M{"action": "queue.items.cancel_requested"}); n != 1 {
		t.Fatalf("audit: %d", n)
	}
}

func TestIntegrationWholeQueueAllOrgs(t *testing.T) {
	h := newHarness(t)
	a1 := h.outbound(orgA, accA, submit.StateQueued, 0, false)
	b1 := h.outbound(orgB, accB, submit.StateScheduled, 0, false)
	sys := h.outbound(orgB, accB, submit.StateQueued, 0, true)
	inflight := h.outbound(orgB, accB, submit.StateSending, 1, false)

	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"all": true, "scope": "all", "confirm": QueueSending}, admin(orgA)); w.Code != 403 {
		t.Fatalf("admin all orgs: %d", w.Code)
	}
	w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"all": true, "scope": "all", "confirm": "emailbridge_submit"}, instanceOwner())
	if w.Code != 422 || !strings.Contains(w.Body.String(), string(errs.CodeQueueConfirmationRequired)) {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"all": true, "scope": "all", "confirm": QueueSending}, instanceOwner())
	if w.Code != 202 {
		t.Fatalf("all: %d %s", w.Code, w.Body.String())
	}
	r := decode[CancelResult](t, w)
	if *r.Matched != 3 || r.Job.OrganizationID != orgA {
		t.Fatalf("matched: %+v", r)
	}
	res := h.runJob(instanceOwner(), r.Job.ID)
	if res.Cancelled != 3 {
		t.Fatalf("job: %+v", res)
	}
	for _, id := range []string{a1, b1, sys} {
		if st := h.stateOf(submit.Collection, id); st != submit.StateCancelled {
			t.Fatalf("%s: %s", id, st)
		}
	}
	if st := h.stateOf(submit.Collection, inflight); st != submit.StateSending {
		t.Fatalf("in flight cancelled: %s", st)
	}
	if got := h.hub.orgs(); got[orgB] == 0 || got[orgA] == 0 {
		t.Fatalf("realtime per org: %+v", got)
	}
	// Organisation-scope whole queue for an admin also needs the confirmation.
	h.outbound(orgA, accA, submit.StateQueued, 0, false)
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"all": true}, admin(orgA)); w.Code != 422 {
		t.Fatalf("org all without confirm: %d", w.Code)
	}
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"all": true, "confirm": QueueSending}, admin(orgA))
	if w.Code != 202 {
		t.Fatalf("org all: %d %s", w.Code, w.Body.String())
	}
	if res := h.runJob(admin(orgA), decode[CancelResult](t, w).Job.ID); res.Cancelled != 1 {
		t.Fatalf("org all job: %+v", res)
	}
}

func TestIntegrationWebhookQueueAndKeyNarrowing(t *testing.T) {
	h := newHarness(t)
	pend := h.delivery(orgA, accA, epA, webhook.StatePending)
	retry := h.delivery(orgA, accX, epA, webhook.StateRetrying)
	dead := h.delivery(orgA, accA, epB, webhook.StateDeadLettered)
	deliv := h.delivery(orgA, accA, epA, webhook.StateDelivering)
	h.delivery(orgA, accA, epA, webhook.StateSucceeded)
	theirs := h.delivery(orgB, accB, epA, webhook.StatePending)

	w := h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_DELIVERY/items?endpointId="+epA, nil, viewer(orgA))
	if p := decode[Page](t, w); w.Code != 200 || p.Total != 3 {
		t.Fatalf("list: %d %+v", w.Code, p)
	}
	// Operators without webhooks.write? Operators have it; viewers do not.
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"ids": []string{pend}}, viewer(orgA)); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"ids": []string{deliv}}, admin(orgA)); w.Code != 409 {
		t.Fatalf("delivering: %d", w.Code)
	}
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"ids": []string{dead}}, admin(orgA)); w.Code != 200 {
		t.Fatalf("dead letter: %d", w.Code)
	}
	if st := h.stateOf(webhook.DeliveriesCollection, dead); st != webhook.StateCancelled {
		t.Fatalf("dead: %s", st)
	}

	// An account-bound API key sees and cancels only its account, also by filter (frozen into the job).
	key := tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_01", OrganizationID: orgA, AccountID: accA,
		Scopes: []string{"webhooks:write", "deliveries:read"}}
	w = h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_DELIVERY/items", nil, key)
	if p := decode[Page](t, w); w.Code != 200 || p.Total != 2 { // pend + deliv (dead now cancelled)
		t.Fatalf("key list: %d %+v", w.Code, p)
	}
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"filter": map[string]any{"endpointId": epA}}, key)
	if w.Code != 202 {
		t.Fatalf("key filter: %d %s", w.Code, w.Body.String())
	}
	r := decode[CancelResult](t, w)
	if *r.Matched != 1 {
		t.Fatalf("key matched: %+v", r)
	}
	h.runJob(key, r.Job.ID)
	if h.stateOf(webhook.DeliveriesCollection, pend) != webhook.StateCancelled ||
		h.stateOf(webhook.DeliveriesCollection, retry) != webhook.StateRetrying ||
		h.stateOf(webhook.DeliveriesCollection, theirs) != webhook.StatePending {
		t.Fatal("key narrowing not applied")
	}
	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"ids": []string{retry}}, key); w.Code != 404 {
		t.Fatalf("other account by id: %d", w.Code)
	}
}

func TestIntegrationSysmailAndEvents(t *testing.T) {
	h := newHarness(t)
	pending := h.sysmailRecord(sysmail.RecordPending, 0)
	retrying := h.sysmailRecord(sysmail.RecordPending, 1)
	sending := h.sysmailRecord(sysmail.RecordSending, 0)
	h.sysmailRecord(sysmail.RecordSent, 0)

	if w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SYSMAIL/items/cancel", map[string]any{"ids": []string{pending}}, admin(orgA)); w.Code != 403 {
		t.Fatalf("org admin: %d", w.Code)
	}
	w := h.do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SYSMAIL/items", nil, instanceOwner())
	p := decode[Page](t, w)
	if w.Code != 200 || p.Total != 3 {
		t.Fatalf("owner list: %d %+v", w.Code, p)
	}
	for _, it := range p.Data {
		if it.OrganizationID != "" || it.MailKind != sysmail.KindInvitation || it.Recipient == "" {
			t.Fatalf("item: %+v", it)
		}
		if it.ID == retrying && it.State != "retrying" {
			t.Fatalf("retrying: %+v", it)
		}
	}
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SYSMAIL/items/cancel", map[string]any{"ids": []string{pending, sending}}, instanceOwner())
	r := decode[CancelResult](t, w)
	if w.Code != 200 || *r.Cancelled != 1 || len(r.Skipped) != 1 || r.Skipped[0].Reason != ReasonInFlight {
		t.Fatalf("cancel: %d %+v", w.Code, r)
	}
	if st := h.stateOf(sysmail.RecordsCollection, pending); st != sysmail.RecordCancelled {
		t.Fatalf("state: %s", st)
	}
	// Counts.
	c, err := h.svc.Counts(tenancy.WithCaller(h.ctx, instanceOwner()), QueueSysmail, false)
	if err != nil || c == nil || c.Retrying != 1 || c.InFlight != 1 || c.Waiting != 0 || c.OldestAt == nil {
		t.Fatalf("counts: %+v %v", c, err)
	}
	if c, _ := h.svc.Counts(tenancy.WithCaller(h.ctx, admin(orgA)), QueueSysmail, false); c != nil {
		t.Fatalf("org admin sees system mail counts: %+v", c)
	}
	if c, _ := h.svc.Counts(h.ctx, QueueEvents, false); c != nil {
		t.Fatal("events counts")
	}

	// Events are read-only.
	w = h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_EVENTS/items/cancel", map[string]any{"all": true, "confirm": QueueEvents}, instanceOwner())
	if w.Code != 409 || !strings.Contains(w.Body.String(), string(errs.CodeQueueReadOnly)) {
		t.Fatalf("events: %d %s", w.Code, w.Body.String())
	}
	if w := h.do(http.MethodPost, "/v1/queues/NOPE/items/cancel", map[string]any{"all": true}, instanceOwner()); w.Code != 404 {
		t.Fatalf("unknown: %d", w.Code)
	}
}

func TestIntegrationCountsScoped(t *testing.T) {
	h := newHarness(t)
	h.outbound(orgA, accA, submit.StateScheduled, 0, false)
	h.outbound(orgA, accA, submit.StateQueued, 0, false)
	h.outbound(orgA, accA, submit.StateQueued, 3, false)
	h.outbound(orgA, accA, submit.StateSending, 1, false)
	h.outbound(orgA, accA, submit.StateQueued, 0, true) // system: not counted for the organisation
	h.outbound(orgB, accB, submit.StateQueued, 0, false)
	h.delivery(orgA, accA, epA, webhook.StateDeadLettered)

	ctx := tenancy.WithCaller(h.ctx, admin(orgA))
	c, err := h.svc.Counts(ctx, QueueSending, false)
	if err != nil || c.Scheduled != 1 || c.Waiting != 1 || c.Retrying != 1 || c.InFlight != 1 || c.OldestAt == nil {
		t.Fatalf("org counts: %+v %v", c, err)
	}
	if c, _ := h.svc.Counts(ctx, QueueSending, true); c != nil {
		t.Fatalf("admin all scope: %+v", c)
	}
	all, err := h.svc.Counts(tenancy.WithCaller(h.ctx, instanceOwner()), QueueSending, true)
	if err != nil || all.Waiting != 3 {
		t.Fatalf("all counts: %+v %v", all, err)
	}
	d, err := h.svc.Counts(ctx, QueueWebhook, false)
	if err != nil || d.DeadLettered != 1 {
		t.Fatalf("webhook counts: %+v %v", d, err)
	}
}

func TestIntegrationJobStopsWhenCancelledAndBadPayload(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		h.outbound(orgA, accA, submit.StateQueued, 0, false)
	}
	w := h.do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"filter": map[string]any{"state": "queued"}}, admin(orgA))
	r := decode[CancelResult](t, w)
	j, err := h.jobs.Get(tenancy.WithCaller(h.ctx, admin(orgA)), r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.svc.HandleCancelJob(h.ctx, j, &rep{cancel: true})
	if err != nil || out.(JobResult).Cancelled != 0 {
		t.Fatalf("cancelled job: %+v %v", out, err)
	}
	for _, payload := range []string{"", "{", `{"queue":"NOPE"}`, `{"queue":"EMAILBRIDGE_SUBMIT","scope":{}}`} {
		bad := &jobs.Job{ID: "job_x", Kind: jobKindCancel, Payload: json.RawMessage(payload)}
		if _, err := h.svc.HandleCancelJob(h.ctx, bad, &rep{}); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("payload %q: %v", payload, err)
		}
	}
	// No job service: a clear internal error.
	h.svc.Jobs = nil
	if _, _, err := h.svc.Cancel(tenancy.WithCaller(h.ctx, admin(orgA)), QueueSending, CancelRequest{Filter: &Filter{State: "queued"}}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no jobs: %v", err)
	}
}
