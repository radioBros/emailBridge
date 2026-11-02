//go:build integration

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationEventsReplayHappyPathAndFilters(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.Events.Retention = func() time.Duration { return 7 * 24 * time.Hour }
	h.service.RegisterHandlers()
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	accA := store.NewID(store.PrefixAccount)
	accB := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "replay-ep", srv.URL, accA)

	evA := h.event(t, accA, "keep-a")
	evB := emitEventAt(t, h, accB, "other-acc", h.now.Add(-time.Minute))
	evOldType := emitTypedEvent(t, h, accA, events.MessageUpdated, "upd", h.now.Add(-30*time.Second))

	job, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID,
		From:       h.now.Add(-time.Hour),
		To:         h.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, job.ID, jobs.Succeeded)
	var result EventsReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Fatalf("expected 1 replayed (account filter), got %+v (evA=%s evB=%s upd=%s)", result, evA.ID, evB.ID, evOldType.ID)
	}

	// ignoreEndpointFilter should include accB + message.updated
	job2, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID:           ep.ID,
		From:                 h.now.Add(-time.Hour),
		To:                   h.now.Add(time.Hour),
		IgnoreEndpointFilter: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, job2.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed < 2 {
		t.Fatalf("ignore filter expected ≥2, got %+v", result)
	}

	// types filter
	job3, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID:           ep.ID,
		From:                 h.now.Add(-time.Hour),
		Types:                []string{events.MessageUpdated},
		IgnoreEndpointFilter: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got = waitJob(t, js, h.ctx, job3.ID, jobs.Succeeded)
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Fatalf("types filter: %+v", result)
	}

	n, err := h.db.Collection(DeliveriesCollection).CountDocuments(h.ctx, map[string]any{
		"organizationId": h.orgID, "endpointId": ep.ID, "isReplay": true, "eventId": evA.ID,
	})
	if err != nil || n < 1 {
		t.Fatalf("replay deliveries for evA: %d %v", n, err)
	}
}

func TestIntegrationEventsReplayWindowValidationAndRBAC(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.Events.Retention = func() time.Duration { return 48 * time.Hour }
	h.service.RegisterHandlers()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	acc := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "win", srv.URL, acc)

	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-72 * time.Hour),
	}); errs.CodeOf(err) != errs.CodeReplayWindowExceeded {
		t.Fatalf("window: %v", err)
	}
	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(time.Hour), To: h.now,
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("from>to: %v", err)
	}
	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		From: h.now.Add(-time.Hour),
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("missing endpoint: %v", err)
	}
	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: store.NewID(store.PrefixEndpoint), From: h.now.Add(-time.Hour),
	}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing ep: %v", err)
	}
	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour), Types: []string{"bad.*.x"},
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad types: %v", err)
	}

	viewer := h.caller(h.orgID, tenancy.RoleViewer)
	if _, err := h.service.ReplayEvents(viewer, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour),
	}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer: %v", err)
	}

	prev := eventsReplayBatchSize
	eventsReplayBatchSize = 1
	t.Cleanup(func() { eventsReplayBatchSize = prev })
	_ = h.event(t, acc, "one")
	_ = h.event(t, acc, "two")
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	job, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, job.ID, jobs.Succeeded)
	var result EventsReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 2 {
		t.Fatalf("batched: %+v", result)
	}
}

func TestIntegrationEventsReplayLinksOriginalAndCancel(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.Events.Retention = func() time.Duration { return 7 * 24 * time.Hour }
	h.service.RegisterHandlers()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	acc := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "link", srv.URL, acc)
	ev := h.event(t, acc, "orig")
	original := insertDeliveryWithEvent(t, h, ep.ID, acc, ev, StateSucceeded)

	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	job, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, job.ID, jobs.Succeeded)
	var result EventsReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Fatalf("result=%+v", result)
	}
	updated, err := h.service.Repo.delivery(h.ctx, original.ID)
	if err != nil || updated.ReplayedAt == nil {
		t.Fatalf("original replayedAt: %+v %v", updated, err)
	}
	var replays []Delivery
	cur, err := h.db.Collection(DeliveriesCollection).Find(h.ctx, map[string]any{
		"organizationId": h.orgID, "replayOf": original.ID, "isReplay": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cur.All(h.ctx, &replays); err != nil || len(replays) != 1 {
		t.Fatalf("replays=%v err=%v", replays, err)
	}
	if replays[0].EventID != ev.ID {
		t.Fatal("webhook-id / eventId mismatch")
	}
}

func TestIntegrationEventsReplayEmptyCancelAndErrors(t *testing.T) {
	h := newWebhookHarness(t)
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: h.service.Log}
	h.service.Jobs = js
	h.service.Events.Retention = func() time.Duration { return 7 * 24 * time.Hour }
	h.service.RegisterHandlers()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	acc := store.NewID(store.PrefixAccount)
	ep := h.endpoint(t, "empty", srv.URL, acc)

	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); js.Run(rctx, 1) }()
	t.Cleanup(func() { cancel(); <-done })

	empty, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour), To: h.now.Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, js, h.ctx, empty.ID, jobs.Succeeded)
	var result EventsReplayResult
	if err := json.Unmarshal(got.Result, &result); err != nil || result.Replayed != 0 {
		t.Fatalf("empty: %+v %v", result, err)
	}

	// no jobs service
	h.service.Jobs = nil
	if _, err := h.service.ReplayEvents(h.ctx, EventsReplayRequest{
		EndpointID: ep.ID, From: h.now.Add(-time.Hour),
	}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no jobs: %v", err)
	}
	h.service.Jobs = js

	_ = h.event(t, acc, "for-cancel-handler")

	// Direct handler edges: bad payload / missing org / cancelled reporter / missing endpoint.
	rep := &stubReporter{}
	if _, err := h.service.handleEventsReplay(h.ctx, &jobs.Job{Payload: []byte(`{`)}, rep); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad payload: %v", err)
	}
	if _, err := h.service.handleEventsReplay(h.ctx, &jobs.Job{Payload: []byte(`{}`)}, rep); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("no org: %v", err)
	}
	rep.cancelled = true
	payload, _ := json.Marshal(EventsReplayRequest{EndpointID: ep.ID, From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)})
	if _, err := h.service.handleEventsReplay(h.ctx, &jobs.Job{
		OrganizationID: h.orgID, Payload: payload,
	}, rep); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancelled: %v", err)
	}
	missingEP := store.NewID(store.PrefixEndpoint)
	body, _ := json.Marshal(EventsReplayRequest{EndpointID: missingEP, From: h.now.Add(-time.Hour)})
	if _, err := h.service.handleEventsReplay(h.ctx, &jobs.Job{
		OrganizationID: h.orgID, Payload: body,
	}, &stubReporter{}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing endpoint in job: %v", err)
	}

	// Skips + publish errors + to-cutoff inside a batch.
	other := store.NewID(store.PrefixAccount)
	_ = emitEventAt(t, h, other, "skip-acc", h.now.Add(-time.Minute))
	_ = emitEventAt(t, h, acc, "after-to", h.now.Add(2*time.Hour))
	prevPub := h.service.Publisher
	h.service.Publisher = nil
	out, err := h.service.handleEventsReplay(h.ctx, &jobs.Job{
		OrganizationID: h.orgID,
		Payload: mustJSON(EventsReplayRequest{
			EndpointID: ep.ID, From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour),
			IgnoreEndpointFilter: true,
		}),
	}, &stubReporter{})
	h.service.Publisher = prevPub
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(EventsReplayResult)
	if !ok || res.Errors < 1 {
		t.Fatalf("expected publish errors: %+v", out)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

type stubReporter struct{ cancelled bool }

func (s *stubReporter) Progress(context.Context, int64, int64) {}
func (s *stubReporter) Cancelled() bool                        { return s.cancelled }

func emitEventAt(t *testing.T, h *webhookHarness, accountID, subject string, at time.Time) events.Event {
	t.Helper()
	messageID := store.NewID(store.PrefixMessage)
	got, err := h.service.Events.Emit(h.ctx, events.Draft{
		Type: events.MessageReceived, OrganizationID: h.orgID, AccountID: accountID,
		Subject: messageID, Time: at,
		Data: &eventtypes.MessageReceived{
			AccountRef: eventtypes.AccountRef{AccountID: accountID},
			MessageID:  messageID, Mailbox: eventtypes.Mailbox{MailboxID: store.NewID(store.PrefixMailbox), Path: "INBOX"},
			Subject: subject, Headers: map[string][]string{"subject": {subject}},
			InternalDate: at,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return got[0]
}

func emitTypedEvent(t *testing.T, h *webhookHarness, accountID, typ, subject string, at time.Time) events.Event {
	t.Helper()
	messageID := store.NewID(store.PrefixMessage)
	var data any
	switch typ {
	case events.MessageUpdated:
		data = &eventtypes.MessageUpdated{
			AccountRef: eventtypes.AccountRef{AccountID: accountID},
			MessageID:  messageID,
			Mailbox:    eventtypes.Mailbox{MailboxID: store.NewID(store.PrefixMailbox), Path: "INBOX"},
			Flags:      eventtypes.FlagChange{},
		}
		_ = subject
	default:
		data = &eventtypes.MessageReceived{
			AccountRef: eventtypes.AccountRef{AccountID: accountID},
			MessageID:  messageID, Mailbox: eventtypes.Mailbox{MailboxID: store.NewID(store.PrefixMailbox), Path: "INBOX"},
			Subject: subject, InternalDate: at,
		}
	}
	got, err := h.service.Events.Emit(h.ctx, events.Draft{
		Type: typ, OrganizationID: h.orgID, AccountID: accountID,
		Subject: messageID, Time: at, Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got[0]
}
