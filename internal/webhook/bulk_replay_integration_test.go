//go:build integration

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationBulkReplayFilterIDsEmptyAndOrg(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.RegisterHandlers()
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	accountID := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "bulk", srv.URL, accountID)

	evKeep := h.event(t, accountID, "keep")
	keep := insertDeliveryWithEvent(t, h, ep.ID, accountID, evKeep, StateFailed)
	evGone := h.event(t, accountID, "gone")
	gone := insertDeliveryWithEvent(t, h, ep.ID, accountID, evGone, StateFailed)
	_, _ = h.db.Collection(events.Collection).DeleteOne(h.ctx, bson.M{"_id": gone.EventID})
	fresh := insertDeliveryWithEvent(t, h, ep.ID, accountID, h.event(t, accountID, "fresh"), StateFailed)

	foreignOrg := store.NewID(store.PrefixOrganization)
	foreign := &Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: foreignOrg,
		AccountID: accountID, EventID: store.NewID(store.PrefixEvent), EventType: events.MessageReceived,
		EndpointID: ep.ID, State: StateFailed, CreatedAt: h.now, UpdatedAt: h.now,
		ExpireAt: h.now.Add(time.Hour), Version: 1,
	}
	if _, err := h.db.Collection(DeliveriesCollection).InsertOne(h.ctx, foreign); err != nil {
		t.Fatal(err)
	}

	if _, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty body: %v", err)
	}
	if _, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{State: StatePending}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("non-final state: %v", err)
	}

	job, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{EndpointID: ep.ID, State: StateFailed})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, job.ID, jobs.Succeeded)
	var result BulkReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed < 1 || result.Skipped < 1 {
		t.Fatalf("result=%+v", result)
	}
	_ = keep

	byIDs, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{IDs: []string{fresh.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, byIDs.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	// fresh was already replayed by the filter job → skipped unless includeReplayed
	if result.Replayed != 0 || result.Skipped != 1 {
		t.Fatalf("ids already replayed: %+v", result)
	}
	byIDs2, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{IDs: []string{fresh.ID}, IncludeReplayed: true})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, byIDs2.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Fatalf("ids includeReplayed: %+v", result)
	}

	foreignCtx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: foreignOrg, Role: tenancy.RoleOwner,
	})
	listed, err := (&Service{Repo: h.service.Repo}).ListDeliveries(foreignCtx, DeliveryFilter{Limit: 10})
	if err != nil || len(listed.Data) != 1 || listed.Data[0].ID != foreign.ID {
		t.Fatalf("foreign org: %+v %v", listed, err)
	}
}

func TestIntegrationBulkReplayStreamingIncludeReplayedAndNonFinal(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.RegisterHandlers()
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	prev := bulkReplayBatchSize
	bulkReplayBatchSize = 50
	t.Cleanup(func() { bulkReplayBatchSize = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	accountID := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "stream-bulk", srv.URL, accountID)

	const n = 120
	for i := 0; i < n; i++ {
		insertDeliveryWithEvent(t, h, ep.ID, accountID, h.event(t, accountID, "s"), StateFailed)
	}
	pending := insertDeliveryWithEvent(t, h, ep.ID, accountID, h.event(t, accountID, "pend"), StatePending)

	job, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{EndpointID: ep.ID, State: StateFailed})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, job.ID, jobs.Succeeded)
	var result BulkReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != n {
		t.Fatalf("stream replayed=%d want %d errors=%d skipped=%d", result.Replayed, n, result.Errors, result.Skipped)
	}

	job2, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{EndpointID: ep.ID, State: StateFailed})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, job2.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 0 {
		t.Fatalf("second run without includeReplayed: %+v", result)
	}

	job3, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{EndpointID: ep.ID, State: StateFailed, IncludeReplayed: true})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, job3.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != n {
		t.Fatalf("includeReplayed: %+v", result)
	}

	idsJob, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{IDs: []string{pending.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, idsJob.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || result.Replayed != 0 || result.Errors != 0 {
		t.Fatalf("non-final ids: %+v", result)
	}
}

func TestIntegrationBulkReplayCancelAndWebhookID(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.RegisterHandlers()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	accountID := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "cancel-bulk", srv.URL, accountID)
	d1 := insertDeliveryWithEvent(t, h, ep.ID, accountID, h.event(t, accountID, "one"), StateSucceeded)

	queued, err := h.service.BulkReplay(h.ctx, BulkReplayFilter{EndpointID: ep.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := js.Cancel(h.ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	got, err := js.Get(h.ctx, queued.ID)
	if err != nil || got.State != jobs.Cancelled {
		t.Fatalf("queued cancel: %+v %v", got, err)
	}

	replay, err := h.service.Replay(h.ctx, d1.ID)
	if err != nil || replay.EventID != d1.EventID || replay.ReplayOf != d1.ID {
		t.Fatalf("replay webhook-id: %+v %v", replay, err)
	}
	orig, err := h.service.GetDelivery(h.ctx, d1.ID)
	if err != nil || orig.ReplayedAt == nil {
		t.Fatalf("replayedAt: %+v %v", orig, err)
	}
}

func TestIntegrationDeliveryListFailedQSortAndRetryDefaults(t *testing.T) {
	h := newWebhookHarness(t)
	set := settings.NewService(h.db, h.service.Cipher, nil, nil)
	h.service.Settings = set

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	accountID := store.NewID(store.PrefixAccount)

	if _, err := set.Patch(h.ctx, "organization", map[string]any{
		"webhook.retryDefaults": map[string]any{
			"maxAttempts": 7, "backoffBase": "2s", "backoffJitter": 0.1, "timeout": "9s",
		},
	}, ""); err != nil {
		t.Fatal(err)
	}
	ep, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "retry-defaults", URL: srv.URL + "/r", Events: []string{events.MessageReceived},
		Secret: testSecret(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Retry.MaxAttempts != 7 || ep.Retry.BackoffBase != "2s" || ep.Retry.Timeout != "9s" {
		t.Fatalf("retry defaults = %+v", ep.Retry)
	}

	if _, err := set.Patch(h.ctx, "organization", map[string]any{
		"webhook.retryDefaults": map[string]any{
			"maxAttempts": 3, "backoffBase": "1s", "backoffJitter": 0.2, "timeout": "5s",
		},
	}, ""); err != nil {
		t.Fatal(err)
	}
	still, err := h.service.GetEndpoint(h.ctx, ep.ID)
	if err != nil || still.Retry.MaxAttempts != 7 || still.Retry.Timeout != "9s" {
		t.Fatalf("existing endpoint must keep retry: %+v %v", still, err)
	}

	failed := insertDeliveryWithEvent(t, h, ep.ID, accountID, h.event(t, accountID, "fail"), StateFailed)
	deadEv := h.event(t, accountID, "dead")
	dead := insertDeliveryWithEvent(t, h, ep.ID, accountID, deadEv, StateDeadLettered)
	_, _ = h.db.Collection(DeliveriesCollection).UpdateOne(h.ctx, bson.M{"_id": dead.ID}, bson.M{
		"$set": bson.M{"eventType": "account.added"},
	})
	at := h.now.Add(time.Minute)
	_, _ = h.db.Collection(DeliveriesCollection).UpdateOne(h.ctx, bson.M{"_id": failed.ID}, bson.M{
		"$set": bson.M{"lastAttemptAt": at, "attempts": []DeliveryAttempt{{At: at, StatusCode: 500, LatencyMs: 3}}},
	})

	listed, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{
		State: StateFailed, Page: 1, Limit: 10, IncludeTotal: true, Sort: "-lastAttemptAt",
	})
	if err != nil || listed.Total == nil || *listed.Total != 1 || listed.Data[0].ID != failed.ID {
		t.Fatalf("state=failed: %+v %v", listed, err)
	}
	both, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{
		State: "failed,deadLettered", Page: 1, Limit: 10, IncludeTotal: true,
	})
	if err != nil || both.Total == nil || *both.Total != 2 {
		t.Fatalf("multi state: %+v %v", both, err)
	}
	byQ, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{Q: "account.added", Limit: 10})
	if err != nil || len(byQ.Data) != 1 || byQ.Data[0].ID != dead.ID {
		t.Fatalf("q: %+v %v", byQ, err)
	}

	viewer := h.caller(h.orgID, tenancy.RoleViewer)
	if _, err := h.service.BulkReplay(viewer, BulkReplayFilter{State: StateFailed}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer bulk: %v", err)
	}
	if _, err := h.service.Replay(viewer, failed.ID); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer replay: %v", err)
	}
}

func insertDeliveryWithEvent(t *testing.T, h *webhookHarness, endpointID, accountID string, ev events.Event, state string) *Delivery {
	t.Helper()
	d := &Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: h.orgID, AccountID: accountID,
		EventID: ev.ID, EventType: ev.Type, EndpointID: endpointID, State: state,
		CreatedAt: h.now, UpdatedAt: h.now, ExpireAt: h.now.Add(24 * time.Hour), Version: 1,
	}
	if _, err := h.db.Collection(DeliveriesCollection).InsertOne(h.ctx, d); err != nil {
		t.Fatal(err)
	}
	return d
}

func waitJob(t *testing.T, js *jobs.Service, ctx context.Context, id, state string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := js.Get(ctx, id)
		if err == nil && j.State == state {
			return j
		}
		time.Sleep(40 * time.Millisecond)
	}
	j, _ := js.Get(ctx, id)
	t.Fatalf("job %s want %s got %+v", id, state, j)
	return nil
}
