//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testSvc(t *testing.T) *Service {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_jobs_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func waitState(t *testing.T, s *Service, ctx context.Context, id, state string) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := s.Get(ctx, id)
		if err == nil && j.State == state {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s not %s: %+v %v", id, state, j, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestIntegrationJobLifecycle(t *testing.T) {
	s := testSvc(t)
	org := store.NewID(store.PrefixOrganization)
	ctx := store.WithOrganization(context.Background(), org)
	s.Handle("demo.ok", func(ctx context.Context, j *Job, r Reporter) (any, error) {
		var p struct{ N int64 }
		_ = json.Unmarshal(j.Payload, &p)
		r.Progress(ctx, p.N, p.N)
		return map[string]int64{"processed": p.N}, nil
	})
	s.Handle("demo.fail", func(context.Context, *Job, Reporter) (any, error) {
		return nil, errs.New(errs.CodeConflict, "nope")
	})
	s.Handle("demo.panic", func(context.Context, *Job, Reporter) (any, error) { panic("boom") })
	ok, err := s.Create(ctx, "demo.ok", map[string]int64{"N": 7})
	if err != nil || ok.State != Queued || ok.OrganizationID != org {
		t.Fatalf("%+v %v", ok, err)
	}
	fail, _ := s.Create(ctx, "demo.fail", nil)
	pan, _ := s.Create(ctx, "demo.panic", nil)
	unknown, _ := s.Create(ctx, "demo.unknown", nil)
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(rctx, 2) }()
	defer func() { cancel(); <-done }()

	j := waitState(t, s, ctx, ok.ID, Succeeded)
	if string(j.Result) != `{"processed":7}` || j.Progress == nil || j.Progress.Done != 7 || j.StartedAt == nil || j.FinishedAt == nil {
		t.Fatalf("succeeded job %+v", j)
	}
	if j := waitState(t, s, ctx, fail.ID, Failed); j.Error == nil || j.Error.Code != errs.CodeConflict {
		t.Fatalf("failed job %+v", j.Error)
	}
	if j := waitState(t, s, ctx, pan.ID, Failed); j.Error == nil {
		t.Fatal("panic must fail the job")
	}
	// a kind without a handler on this node stays queued for a node that has one
	time.Sleep(1500 * time.Millisecond)
	if j, _ := s.Get(ctx, unknown.ID); j.State != Queued {
		t.Fatalf("unknown kind: %s", j.State)
	}
	// tenant isolation
	if _, err := s.Get(store.WithOrganization(context.Background(), store.NewID(store.PrefixOrganization)), ok.ID); err == nil {
		t.Fatal("another organisation read the job")
	}
	if err := s.Cancel(ctx, ok.ID); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancel finished job: %v", err)
	}
}

func TestIntegrationCancel(t *testing.T) {
	s := testSvc(t)
	ctx := context.Background()
	started := make(chan struct{})
	s.Handle("demo.slow", func(ctx context.Context, _ *Job, r Reporter) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	queued, _ := s.Create(ctx, "demo.wait", nil) // no runner yet: cancel while queued
	if err := s.Cancel(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(ctx, queued.ID); j.State != Cancelled {
		t.Fatalf("queued cancel: %s", j.State)
	}
	slow, _ := s.Create(ctx, "demo.slow", nil)
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.Run(rctx, 1) }()
	defer func() { cancel(); <-done }()
	<-started
	if err := s.Cancel(ctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	// the heartbeat (10 s) notices the request; shorten the wait by checking the flag directly
	var raw bson.M
	_ = s.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": slow.ID}).Decode(&raw)
	if raw["cancelRequested"] != true {
		t.Fatal("cancel request not recorded")
	}
	waitStateLong(t, s, ctx, slow.ID, Cancelled, 20*time.Second)
}

func waitStateLong(t *testing.T, s *Service, ctx context.Context, id, state string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		j, err := s.Get(ctx, id)
		if err == nil && j.State == state {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s not %s: %+v %v", id, state, j, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIntegrationStaleRunningIsRequeued(t *testing.T) {
	s := testSvc(t)
	ctx := context.Background()
	var runs atomic.Int32
	s.Handle("demo.idem", func(context.Context, *Job, Reporter) (any, error) { runs.Add(1); return nil, nil })
	j, _ := s.Create(ctx, "demo.idem", nil)
	// a runner on a dead node claimed it two minutes ago
	old := time.Now().UTC().Add(-2 * time.Minute)
	if _, err := s.DB.Collection(Collection).UpdateOne(ctx, bson.M{"_id": j.ID}, bson.M{"$set": bson.M{"state": Running, "runner": "dead", "heartbeatAt": old, "attempts": 1}}); err != nil {
		t.Fatal(err)
	}
	// and one that exhausted its attempts
	k, _ := s.Create(ctx, "demo.idem", nil)
	_, _ = s.DB.Collection(Collection).UpdateOne(ctx, bson.M{"_id": k.ID}, bson.M{"$set": bson.M{"state": Running, "runner": "dead", "heartbeatAt": old, "attempts": maxAttempts}})
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.Run(rctx, 1) }()
	defer func() { cancel(); <-done }()
	waitState(t, s, ctx, j.ID, Succeeded)
	waitState(t, s, ctx, k.ID, Failed)
	if runs.Load() != 1 {
		t.Fatalf("runs %d", runs.Load())
	}
	if !errors.Is(context.Canceled, context.Canceled) {
		t.Fatal()
	}
}

func TestIntegrationModuleAndAPI(t *testing.T) {
	s := testSvc(t)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: s.Log})
	app.Provide(a, s.DB)
	m := &Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	svc, _ := app.MustGet[*Service](a)
	org := store.NewID(store.PrefixOrganization)
	ctx := store.WithOrganization(context.Background(), org)
	j, err := svc.Create(ctx, "demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.getJob(ctx, &getJobIn{JobID: j.ID}); err == nil {
		t.Fatal("no caller must be refused")
	}
	cctx := tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_x", OrganizationID: org, Role: tenancy.RoleViewer})
	out, err := m.getJob(cctx, &getJobIn{JobID: j.ID})
	if err != nil || out.Body.ID != j.ID || out.Body.State != Queued {
		t.Fatalf("%v %+v", err, out)
	}
	if m.Name() != "jobs" || m.Roles() != nil || m.Stop(context.Background()) != nil {
		t.Fatal("identity")
	}
}

func TestIntegrationRunNow(t *testing.T) {
	s := testSvc(t)
	org := store.NewID(store.PrefixOrganization)
	s.Handle("sub", func(ctx context.Context, j *Job, r Reporter) (any, error) {
		got, _ := store.OrganizationFrom(ctx)
		var p map[string]string
		_ = json.Unmarshal(j.Payload, &p)
		r.Progress(ctx, 1, 1)
		if r.Cancelled() {
			return nil, context.Canceled
		}
		return got + "/" + p["x"], nil
	})
	out, err := s.RunNow(context.Background(), "sub", org, map[string]string{"x": "y"}, func() bool { return false })
	if err != nil || out != org+"/y" {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := s.RunNow(context.Background(), "sub", org, nil, func() bool { return true }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := s.RunNow(context.Background(), "missing", "", nil, nil); err == nil {
		t.Fatal("unknown kind")
	}
	if n, _ := s.DB.Collection(Collection).CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Fatal("inline runs are not stored")
	}
}
