//go:build integration

package submit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// failingPublisher stands in for a bus that is down when the API accepts a message (S6 §2.7).
type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, []byte, string, map[string]string) (bool, error) {
	return false, errors.New("bus is down")
}

func TestIntegrationWorkerJobHandling(t *testing.T) {
	h := newHarness(t)
	// A job that is not JSON, or incomplete, is poison and must be terminated, not redelivered.
	for _, payload := range []string{"not json", `{"outboundId":"out_1"}`, `{"organizationId":"org_1"}`} {
		err := h.svc.Work(h.ctx, bus.Message{Data: []byte(payload)})
		var discard *bus.Discard
		if !errors.As(err, &discard) {
			t.Fatalf("job %q: %v", payload, err)
		}
	}
	// A job for a message that no longer exists is acknowledged.
	gone, err := json.Marshal(job{OutboundID: store.NewID(store.PrefixOutbound), OrganizationID: h.org})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Work(h.ctx, bus.Message{Data: gone}); err != nil {
		t.Fatalf("erased message: %v", err)
	}
	// A message another worker already claimed is left alone.
	id := h.submit(SubmitRequest{})
	m := h.get(id)
	if _, err := h.svc.Repo.claim(h.ctx, h.org, id, "other-node", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.attempt(h.ctx, m); err != nil {
		t.Fatalf("a lost claim must ack: %v", err)
	}
	if got := h.get(id); got.Worker != "other-node" || len(got.Attempts) != 0 {
		t.Fatalf("the claim was stolen: %+v", got)
	}
}

func TestIntegrationFailuresBeforeTheSession(t *testing.T) {
	h := newHarness(t)
	// The account disappeared between submit and send.
	id := h.submit(SubmitRequest{})
	h.accounts.err = errs.New(errs.CodeNotFound, "The account does not exist.")
	h.work()
	m := h.get(id)
	if m.State != StateQueued || len(m.Attempts) != 1 || m.Attempts[0].Stage != "connect" {
		t.Fatalf("missing account: %+v", m)
	}
	h.accounts.err = nil

	// The account now belongs to another organisation.
	other := *h.acc
	other.OrganizationID = store.NewID(store.PrefixOrganization)
	h.accounts.put(&other)
	if err := h.redeliver(id); err != nil {
		t.Fatal(err)
	}
	if m = h.get(id); len(m.Attempts) != 2 {
		t.Fatalf("foreign account: %+v", m.Attempts)
	}
	h.accounts.put(h.acc)

	// The built message vanished from object storage.
	second := h.submit(SubmitRequest{})
	h.objects.mu.Lock()
	h.objects.data = map[string][]byte{}
	h.objects.mu.Unlock()
	h.work()
	if m = h.get(second); m.State != StateQueued || len(m.Attempts) != 1 {
		t.Fatalf("missing raw message: %+v", m)
	}
	if m.Attempts[0].Kind != string(errs.KindTemporary) {
		t.Fatalf("a storage failure is temporary: %+v", m.Attempts[0])
	}
}

func TestIntegrationPublishFailureLeavesTheMessageDue(t *testing.T) {
	h := newHarness(t)
	h.svc.Publisher = failingPublisher{}
	res, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "x",
	}, "", false)
	if err != nil {
		t.Fatalf("a publish failure must not fail the request: %v", err)
	}
	m := h.get(res.Accepted.ID)
	if m.State != StateQueued || m.NextAttemptAt == nil || !m.NextAttemptAt.Before(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("the message must stay due for the scheduler: %+v", m)
	}
	// The due duty picks it up and the worker sends it.
	h.svc.Publisher = h.pub
	h.runDue()
	h.work()
	if m = h.get(res.Accepted.ID); m.State != StateSent {
		t.Fatalf("state: %s", m.State)
	}
}

func TestIntegrationCopyToSentWithoutACopier(t *testing.T) {
	h := newHarness(t)
	h.svc.Sent = nil
	id := h.submit(SubmitRequest{})
	h.work()
	m := h.get(id)
	if m.State != StateSent || m.CopyToSent.State != CopySkipped {
		t.Fatalf("without a copier the copy is skipped: %+v", m.CopyToSent)
	}
}

func TestIntegrationIdempotencyRaceReplay(t *testing.T) {
	h := newHarness(t)
	req := SubmitRequest{To: []AddressInput{{Address: testTo}}, Text: "race"}
	first, err := h.svc.Submit(h.ctx, h.acc.ID, req, "race-1", false)
	if err != nil {
		t.Fatal(err)
	}
	idemID, hash, err := h.svc.idempotencyOf(h.org, h.acc.ID, "race-1", req)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11000, Message: "duplicate key"}}}
	res, err := h.svc.replayConflict(h.ctx, duplicate, idemID, hash)
	if err != nil || res == nil || res.Accepted.ID != first.Accepted.ID {
		t.Fatalf("the twin request must replay: %+v (%v)", res, err)
	}
	// A different body is a conflict, and an unfinished twin is "in progress".
	if _, err := h.svc.replayConflict(h.ctx, duplicate, idemID, "other-hash"); errs.CodeOf(err) != errs.CodeIdempotencyKeyReused {
		t.Fatalf("different body: %v", err)
	}
	if _, err := h.svc.replayConflict(h.ctx, duplicate, idempotencyID(h.org, h.acc.ID, "POST", submitRoute, "unknown"), hash); errs.CodeOf(err) != errs.CodeIdempotencyInProgress {
		t.Fatalf("unfinished twin: %v", err)
	}
	// Errors that are not duplicate keys are passed through untouched.
	if res, err := h.svc.replayConflict(h.ctx, errors.New("other"), idemID, hash); res != nil || err != nil {
		t.Fatalf("unrelated error: %+v (%v)", res, err)
	}
	// A stored response that is not an acceptance cannot be replayed.
	broken := &idempotencyRecord{RequestHash: hash, Response: "{"}
	if _, err := replayOf(broken, hash); errs.CodeOf(err) != errs.CodeIdempotencyInProgress {
		t.Fatal("a broken record must not replay")
	}
}

func TestIntegrationHeartbeatAndStaleSweep(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	now := time.Now().UTC()
	if _, err := h.svc.Repo.claim(h.ctx, h.org, id, h.svc.NodeID, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	if err := h.svc.Repo.heartbeat(h.ctx, id, h.svc.NodeID, later); err != nil {
		t.Fatal(err)
	}
	m := h.get(id)
	if m.HeartbeatAt == nil || !m.HeartbeatAt.Equal(later.Truncate(time.Millisecond)) {
		t.Fatalf("heartbeat: %v", m.HeartbeatAt)
	}
	if h.svc.isStale(m) {
		t.Fatal("a fresh heartbeat is not stale")
	}
	// A heartbeat of another worker does not move.
	if err := h.svc.Repo.heartbeat(h.ctx, id, "someone-else", later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := h.get(id); !got.HeartbeatAt.Equal(m.HeartbeatAt.UTC()) {
		t.Fatal("another worker refreshed the heartbeat")
	}
	// Without a heartbeat the message is stale at once.
	if _, err := h.db.Collection(Collection).UpdateOne(h.ctx, bson.M{"_id": id},
		bson.M{"$unset": bson.M{"heartbeatAt": ""}}); err != nil {
		t.Fatal(err)
	}
	if !h.svc.isStale(h.get(id)) {
		t.Fatal("a message without a heartbeat is stale")
	}
	// recoverStale on a message that has no attempt yet opens one.
	if err := h.redeliver(id); err != nil {
		t.Fatal(err)
	}
	if m = h.get(id); m.State != StateQueued || len(m.Attempts) != 1 {
		t.Fatalf("recovered: %+v", m)
	}
}

func TestIntegrationPoolSweepAndRun(t *testing.T) {
	h := newHarness(t)
	h.svc.Pool.Idle = time.Nanosecond
	lease, err := h.svc.Pool.Acquire(h.ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease.Release() // releasing twice is a no-op
	h.svc.Pool.Sweep()
	if next, err := h.svc.Pool.Acquire(h.ctx, h.acc); err != nil {
		t.Fatal(err)
	} else if next.Session.Messages() != 0 {
		t.Fatal("the sweep must have closed the idle session")
	} else {
		next.Discard()
		next.Discard()
	}
	ctx, cancel := context.WithCancel(h.ctx)
	done := make(chan struct{})
	go func() { defer close(done); h.svc.Pool.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Pool.Run did not stop")
	}
	// A dial failure releases the slot and surfaces the error.
	broken := *h.acc
	broken.Port = 1
	if _, err := h.svc.Pool.Acquire(h.ctx, &broken); err == nil {
		t.Fatal("a refused connection must fail Acquire")
	}
	if lease, err := h.svc.Pool.Acquire(h.ctx, h.acc); err != nil {
		t.Fatalf("the slot was not released: %v", err)
	} else {
		lease.Discard()
	}
}

// TestIntegrationModuleWiring starts the module the way cmd does and sends one message through the
// embedded bus, the durable consumer and the registered API.
func TestIntegrationModuleWiring(t *testing.T) {
	h := newHarness(t)
	eventBus, err := bus.Open(h.ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "submit-test", DataDir: t.TempDir()})
	if err != nil {
		t.Skipf("no embedded NATS: %v", err)
	}
	t.Cleanup(eventBus.Close)
	objects, err := objstore.Open(objstore.Options{Endpoint: "127.0.0.1:9", Bucket: "eb-test"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.New(httpapi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	application := app.New(app.Options{
		Roles:  app.RoleSet{app.RoleAPI: true, app.RoleSubmit: true},
		NodeID: "submit-test", Log: h.svc.Log,
	})
	app.Provide(application, h.db)
	app.Provide(application, objects)
	app.Provide(application, h.svc.Events)
	app.Provide(application, eventBus)
	app.Provide(application, egress.NewDialer(egress.Policy{}))
	app.Provide(application, server)
	app.Provide(application, &scheduler.Scheduler{})
	module := &Module{
		Accounts: h.accounts, Sent: h.sent, Concurrency: 2, MaxMessageSize: 1 << 20,
		MaxAttempts: 3, BackoffBase: time.Millisecond, PoolPerAccount: 2, Log: h.svc.Log,
	}
	if err := module.Start(h.ctx, application); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Stop(context.Background()) })
	if module.Name() != "submit" || len(module.Roles()) != 2 || module.Service() == nil {
		t.Fatal("module identity")
	}
	// The module's own service uses the real object store; the test keeps the in-memory one.
	service := module.Service()
	service.Objects = h.objects
	service.Pool.Dial = service.dial(egress.NewDialer(egress.Policy{}))

	res, err := service.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Subject: "Module", Text: "wired",
	}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if m := h.get(res.Accepted.ID); m.State == StateSent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message is still %s", h.get(res.Accepted.ID).State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if h.delivered() != 1 {
		t.Fatalf("delivered %d", h.delivered())
	}
}
