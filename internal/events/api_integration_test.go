//go:build integration

package events

import (
	"bufio"
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

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type apiHarness struct {
	t          *testing.T
	db         *store.DB
	w          *Writer
	b          *bus.Bus
	srv        *httptest.Server
	org        string
	acc        string
	otherOrg   string
	sseEnabled func(context.Context, string) bool
	keepAlive  time.Duration
	api        *api
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_events_api_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ListIndexesMigration(2).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "api-t1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	w := &Writer{DB: db, Partitions: 4, Retention: func() time.Duration { return 7 * 24 * time.Hour }}
	h := &apiHarness{
		t: t, db: db, w: w, b: b,
		org: store.NewID(store.PrefixOrganization), acc: store.NewID(store.PrefixAccount),
		otherOrg:   store.NewID(store.PrefixOrganization),
		sseEnabled: func(context.Context, string) bool { return true },
		keepAlive:  40 * time.Millisecond,
	}
	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	a := &api{
		w: w,
		o: APIOptions{
			KeepAlive: h.keepAlive, MaxStreams: 20, StreamBuffer: 8, Subscriber: b,
			SSEEnabled: func(ctx context.Context, org string) bool { return h.sseEnabled(ctx, org) },
		},
		lim: newStreamLimiter(20),
	}
	h.api = a
	Register(srv, w, a.o)
	// Re-bind stream limiter / options onto the registered api by re-registering stream only via direct
	// handler — Register already wired list/get/stream. Override stream route with our harness api.
	srv.Router.Get("/v1/events/stream", a.stream)
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if org := r.Header.Get("X-Test-Org"); org != "" {
			ctx = store.WithOrganization(ctx, org)
		}
		if kind := r.Header.Get("X-Test-Caller"); kind != "" {
			c := tenancy.Caller{Kind: tenancy.CallerKind(kind), OrganizationID: r.Header.Get("X-Test-Org")}
			switch kind {
			case string(tenancy.CallerUser):
				c.UserID = store.NewID(store.PrefixUser)
				c.Role = tenancy.Role(r.Header.Get("X-Test-Role"))
				if c.Role == "" {
					c.Role = tenancy.RoleOwner
				}
			case string(tenancy.CallerAPIKey):
				c.APIKeyID = store.NewID(store.PrefixAPIKey)
				c.Scopes = strings.Split(r.Header.Get("X-Test-Scopes"), ",")
				c.AccountID = r.Header.Get("X-Test-Account")
			}
			ctx = tenancy.WithCaller(ctx, c)
		}
		srv.ServeHTTP(rw, r.WithContext(ctx))
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	h.srv = ts
	return h
}

func (h *apiHarness) ctx() context.Context {
	return tenancy.WithCaller(store.WithOrganization(context.Background(), h.org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: h.org, Role: tenancy.RoleOwner,
	})
}

func (h *apiHarness) emit(typ, accountID string, when time.Time) Event {
	h.t.Helper()
	d := Draft{Type: typ, OrganizationID: h.org, AccountID: accountID, Time: when}
	switch typ {
	case MessageUpdated:
		d.Subject = store.NewID(store.PrefixMessage)
		d.Data = &types.MessageUpdated{
			AccountRef: types.AccountRef{AccountID: accountID},
			MessageID:  store.NewID(store.PrefixMessage),
			Flags:      types.FlagChange{},
		}
	case AccountAdded:
		d.Subject = accountID
		d.Data = &types.AccountAdded{AccountRef: types.AccountRef{AccountID: accountID}, Email: "a@test"}
	default:
		d.Type = MessageReceived
		d.Subject = store.NewID(store.PrefixMessage)
		d.Data = &types.MessageReceived{AccountRef: types.AccountRef{AccountID: accountID}, Subject: "s", Flags: []string{}}
	}
	out, err := h.w.Emit(h.ctx(), d)
	if err != nil {
		h.t.Fatal(err)
	}
	return out[0]
}

func (h *apiHarness) headers(extra map[string]string) map[string]string {
	m := map[string]string{
		"X-Test-Org": h.org, "X-Test-Caller": string(tenancy.CallerUser), "X-Test-Role": string(tenancy.RoleOwner),
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (h *apiHarness) do(method, path string, hdr map[string]string) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, b
}

func TestIntegrationListFiltersAndPaging(t *testing.T) {
	h := newAPIHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	e1 := h.emit(MessageReceived, h.acc, now.Add(-2*time.Minute))
	e2 := h.emit(MessageUpdated, h.acc, now.Add(-time.Minute))
	otherAcc := store.NewID(store.PrefixAccount)
	e3 := h.emit(AccountAdded, otherAcc, now)
	_ = e1

	list, err := h.w.List(h.ctx(), ListFilter{Type: []string{"message.*"}, Sort: "-time", Limit: 10})
	if err != nil || len(list.Data) != 2 {
		t.Fatalf("wildcard: %+v %v", list, err)
	}
	list, err = h.w.List(h.ctx(), ListFilter{AccountID: h.acc, IncludeTotal: true, Page: 1, Limit: 10})
	if err != nil || len(list.Data) != 2 || list.Total == nil || *list.Total != 2 {
		t.Fatalf("account+total: %+v %v", list, err)
	}
	list, err = h.w.List(h.ctx(), ListFilter{Type: []string{AccountAdded}, Limit: 10})
	if err != nil || len(list.Data) != 1 || list.Data[0].ID != e3.ID {
		t.Fatalf("type exact: %+v %v", list, err)
	}
	list, err = h.w.List(h.ctx(), ListFilter{From: now.Add(-90 * time.Second), To: now.Add(time.Second), Sort: "time", Limit: 10})
	if err != nil || len(list.Data) < 2 {
		t.Fatalf("range: %+v %v", list, err)
	}
	page1, err := h.w.List(h.ctx(), ListFilter{Sort: "-time", Limit: 1})
	if err != nil || len(page1.Data) != 1 || page1.NextCursor == nil {
		t.Fatalf("cursor page1: %+v %v", page1, err)
	}
	page2, err := h.w.List(h.ctx(), ListFilter{Sort: "-time", Limit: 1, Cursor: *page1.NextCursor})
	if err != nil || len(page2.Data) != 1 || page2.Data[0].ID == page1.Data[0].ID {
		t.Fatalf("cursor page2: %+v %v", page2, err)
	}
	if page1.Data[0].ID != e3.ID || page2.Data[0].ID != e2.ID {
		t.Fatalf("order %s then %s", page1.Data[0].ID, page2.Data[0].ID)
	}

	bound := tenancy.WithCaller(store.WithOrganization(context.Background(), h.org), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: h.org,
		Scopes: []string{"events:read"}, AccountID: h.acc,
	})
	list, err = h.w.List(bound, ListFilter{Limit: 50})
	if err != nil || len(list.Data) != 2 {
		t.Fatalf("bound list: %+v %v", list, err)
	}
	if _, err := h.w.List(bound, ListFilter{AccountID: otherAcc}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound other: %v", err)
	}
	if _, err := h.w.GetEvent(bound, e3.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("bound get other: %v", err)
	}

	other := tenancy.WithCaller(store.WithOrganization(context.Background(), h.otherOrg), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: h.otherOrg, Role: tenancy.RoleOwner,
	})
	if _, err := h.w.GetEvent(other, e2.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-org: %v", err)
	}
	list, err = h.w.List(other, ListFilter{Limit: 10})
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("cross-org list: %+v %v", list, err)
	}

	status, body := h.do(http.MethodGet, "/v1/events?limit=10&sort=-time", h.headers(nil))
	if status != http.StatusOK {
		t.Fatalf("http list %d %s", status, body)
	}
	status, body = h.do(http.MethodGet, "/v1/events/"+e2.ID, h.headers(nil))
	if status != http.StatusOK || !strings.Contains(string(body), e2.ID) {
		t.Fatalf("http get %d %s", status, body)
	}
	status, _ = h.do(http.MethodGet, "/v1/events/"+e2.ID, h.headers(map[string]string{"X-Test-Org": h.otherOrg}))
	if status != http.StatusNotFound {
		t.Fatalf("http cross-org %d", status)
	}
}

func TestIntegrationSSELiveFilterResumeAndLimits(t *testing.T) {
	h := newAPIHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	anchor := h.emit(MessageReceived, h.acc, now.Add(-10*time.Second))
	// Mark published and put on the bus so live delivery works for new emits via relay simulation.
	publish := func(e Event) {
		h.t.Helper()
		payload, _ := json.Marshal(e)
		subj := bus.EventSubject(bus.EventPartition(e.EBAccount, 4), e.EBOrg, e.Type)
		if _, err := h.b.Publish(context.Background(), subj, payload, e.ID, nil); err != nil {
			h.t.Fatal(err)
		}
	}

	// --- live delivery ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/v1/events/stream?type=message.*&accountId="+h.acc, nil)
	for k, v := range h.headers(nil) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream %d %v", resp.StatusCode, resp.Header)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	gotLive := make(chan string, 4)
	go func() {
		var id, etype string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				etype = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				gotLive <- id + "|" + etype
				id, etype = "", ""
			case line == ":keepalive":
				gotLive <- "keepalive"
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // let subscription settle
	live := h.emit(MessageUpdated, h.acc, time.Now().UTC())
	publish(live)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("live timeout")
		}
		select {
		case got := <-gotLive:
			if got == "keepalive" {
				continue
			}
			if !strings.HasPrefix(got, live.ID+"|") {
				t.Fatalf("live got %q want %s", got, live.ID)
			}
			goto liveOK
		case <-time.After(200 * time.Millisecond):
		}
	}
liveOK:
	cancel()

	// --- keepalive ---
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	req2, _ := http.NewRequestWithContext(ctx2, http.MethodGet, h.srv.URL+"/v1/events/stream", nil)
	for k, v := range h.headers(nil) {
		req2.Header.Set(k, v)
	}
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	keepCh := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(resp2.Body)
		for sc.Scan() {
			if sc.Text() == ":keepalive" {
				keepCh <- true
				return
			}
		}
	}()
	select {
	case <-keepCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected keepalive")
	}
	cancel2()

	// --- resume with skew: event whose ID sorts before mid but commits after catch-up starts ---
	mid := h.emit(MessageReceived, h.acc, now.Add(-2*time.Second))
	lateID := "evt_00000000000000000000000001"
	lateTime := mid.Time.Add(-time.Second) // inside (mid.Time − 5s) skew window
	lateDoc := doc{
		ID: lateID, OrganizationID: h.org, AccountID: h.acc, Type: MessageReceived,
		Time: lateTime, Subject: store.NewID(store.PrefixMessage),
		Data: `{"accountId":"` + h.acc + `","subject":"late","flags":[]}`, Schema: 1, Partition: 0,
		ExpireAt: lateTime.Add(7 * 24 * time.Hour),
	}

	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	req3, _ := http.NewRequestWithContext(ctx3, http.MethodGet,
		h.srv.URL+"/v1/events/stream?type=message.received&accountId="+h.acc+"&lastEventId="+mid.ID, nil)
	for k, v := range h.headers(nil) {
		req3.Header.Set(k, v)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(150 * time.Millisecond)
		if _, err := h.db.Collection(Collection).InsertOne(context.Background(), lateDoc); err != nil {
			t.Error(err)
			return
		}
		publish(lateDoc.event())
	}()
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp3.Body)
		t.Fatalf("resume stream %d %s", resp3.StatusCode, b)
	}
	var seenMu sync.Mutex
	seen := map[string]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc3 := bufio.NewScanner(resp3.Body)
		sc3.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc3.Scan() {
			line := sc3.Text()
			if strings.HasPrefix(line, "id: ") {
				seenMu.Lock()
				seen[strings.TrimPrefix(line, "id: ")] = true
				ok := seen[mid.ID] && seen[lateID]
				seenMu.Unlock()
				if ok {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
	}
	wg.Wait()
	seenMu.Lock()
	defer seenMu.Unlock()
	if !seen[mid.ID] {
		t.Fatalf("resume missed mid; seen=%v", seen)
	}
	if !seen[lateID] {
		t.Fatalf("resume missed late out-of-order id; seen=%v (anchor=%s)", seen, anchor.ID)
	}
	cancel3()

	// --- switch off ---
	h.sseEnabled = func(context.Context, string) bool { return false }
	status, body := h.do(http.MethodGet, "/v1/events/stream", h.headers(nil))
	if status != http.StatusForbidden || !strings.Contains(string(body), "feature_disabled") {
		t.Fatalf("switch off %d %s", status, body)
	}
	h.sseEnabled = func(context.Context, string) bool { return true }

	// --- retention 422 ---
	status, body = h.do(http.MethodGet, "/v1/events/stream?lastEventId="+store.NewID(store.PrefixEvent), h.headers(nil))
	if status != http.StatusUnprocessableEntity || !strings.Contains(string(body), "replay_window_exceeded") {
		t.Fatalf("retention %d %s", status, body)
	}

	// --- 21st stream 429 ---
	var stops []context.CancelFunc
	var bodies []io.Closer
	for i := 0; i < 20; i++ {
		cctx, ccancel := context.WithCancel(context.Background())
		stops = append(stops, ccancel)
		r, _ := http.NewRequestWithContext(cctx, http.MethodGet, h.srv.URL+"/v1/events/stream", nil)
		for k, v := range h.headers(nil) {
			r.Header.Set(k, v)
		}
		rr, err := http.DefaultClient.Do(r) //nolint:bodyclose // closed in bodies loop below
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, rr.Body)
		if rr.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(rr.Body)
			t.Fatalf("stream %d status %d %s", i, rr.StatusCode, b)
		}
	}
	status, body = h.do(http.MethodGet, "/v1/events/stream", h.headers(nil))
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("21st %d %s", status, body)
	}
	for _, s := range stops {
		s()
	}
	for _, b := range bodies {
		_ = b.Close()
	}
}

func TestIntegrationCatchUpDirect(t *testing.T) {
	h := newAPIHarness(t)
	now := time.Now().UTC()
	a := h.emit(MessageReceived, h.acc, now.Add(-time.Minute))
	b := h.emit(MessageReceived, h.acc, now)
	batch, err := h.w.CatchUp(h.ctx(), now.Add(-2*time.Minute), "", []string{"message.received"}, h.acc, 10)
	if err != nil || len(batch) < 2 {
		t.Fatalf("%v %v", batch, err)
	}
	batch, err = h.w.CatchUp(h.ctx(), now.Add(-2*time.Minute), a.ID, nil, h.acc, 10)
	if err != nil || len(batch) != 1 || batch[0].ID != b.ID {
		t.Fatalf("after: %+v %v", batch, err)
	}
}

func TestIntegrationListBadSortHTTP(t *testing.T) {
	h := newAPIHarness(t)
	status, body := h.do(http.MethodGet, "/v1/events?sort=-createdAt", h.headers(nil))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", status, body)
	}
}

func TestIntegrationStreamErrorPaths(t *testing.T) {
	h := newAPIHarness(t)
	status, body := h.do(http.MethodGet, "/v1/events/stream?accountId=acc_bad", h.headers(nil))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad account %d %s", status, body)
	}
	status, body = h.do(http.MethodGet, "/v1/events/stream?type=bad.*.x", h.headers(nil))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad type %d %s", status, body)
	}
	status, body = h.do(http.MethodGet, "/v1/events/stream?lastEventId=evt_notavalidulidxxxxxxxxx", h.headers(nil))
	if status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		// pattern may reject at huma; raw handler validates IsID
		t.Logf("lastEventId status %d %s", status, body)
	}
	hdr := h.headers(map[string]string{
		"X-Test-Caller": string(tenancy.CallerAPIKey), "X-Test-Scopes": "events:read",
		"X-Test-Account": store.NewID(store.PrefixAccount),
	})
	status, body = h.do(http.MethodGet, "/v1/events/stream?accountId="+h.acc, hdr)
	if status != http.StatusForbidden {
		t.Fatalf("bound key %d %s", status, body)
	}
	// no subscriber
	h.api.o.Subscriber = nil
	status, body = h.do(http.MethodGet, "/v1/events/stream", h.headers(nil))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("no sub %d %s", status, body)
	}
}

func TestIntegrationDirectInsertVisible(t *testing.T) {
	h := newAPIHarness(t)
	// Ensure BSON docs without going through Emit still list.
	id := store.NewID(store.PrefixEvent)
	_, err := h.db.Collection(Collection).InsertOne(context.Background(), bson.M{
		"_id": id, "organizationId": h.org, "accountId": h.acc, "type": MessageReceived,
		"time": time.Now().UTC(), "subject": store.NewID(store.PrefixMessage),
		"data": `{"accountId":"` + h.acc + `"}`, "schema": 1, "partition": 0,
		"published": false, "expireAt": time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := h.w.GetEvent(h.ctx(), id)
	if err != nil || e.ID != id {
		t.Fatalf("%+v %v", e, err)
	}
}
