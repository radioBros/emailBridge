package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestVisibleToHidesInstanceProblemsFromNonOwners(t *testing.T) {
	inst := realtime.Message{Keys: map[string]string{"scope": scopeKey(&Notification{Scope: ScopeInstance})}}
	org := realtime.Message{Keys: map[string]string{"scope": scopeKey(&Notification{Scope: "org_1"})}}
	viewer := tenancy.Caller{Kind: tenancy.CallerUser, Role: tenancy.RoleViewer}
	owner := tenancy.Caller{Kind: tenancy.CallerUser, Role: tenancy.RoleOwner, InstanceOwner: true}
	if VisibleTo(viewer, inst) || !VisibleTo(viewer, org) || !VisibleTo(owner, inst) || !VisibleTo(owner, org) {
		t.Fatal("visibility rule")
	}
}

func TestCapturePanicRaisesInternalProblem(t *testing.T) {
	e := NewEngine(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.CapturePanic(context.Background(), "boom", map[string]string{"component": "http"})
	e.CapturePanic(context.Background(), errors.New("typed"), nil)
	e.CapturePanic(context.Background(), nil, nil)
	if len(e.ch) != 2 {
		t.Fatalf("queued raises = %d", len(e.ch))
	}
	o := <-e.ch
	if o.p.Category != CatInternal || o.p.Scope != ScopeInstance || o.p.Data["code"] != "panic" {
		t.Fatalf("panic problem: %+v", o.p)
	}
	var nilEngine *Engine
	nilEngine.CapturePanic(context.Background(), "x", nil)
}

func TestDigestPeriod(t *testing.T) {
	if digestPeriod("immediate") != 0 || digestPeriod("daily") != 24*time.Hour || digestPeriod("hourly") != time.Hour || digestPeriod("") != time.Hour {
		t.Fatal("digest periods")
	}
}

func TestEveryRunsAtOnceThenPerInterval(t *testing.T) {
	old := pollEvery
	pollEvery = 10 * time.Millisecond
	defer func() { pollEvery = old }()
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		every(ctx, func(context.Context) time.Duration { return 30 * time.Millisecond }, func(context.Context) { runs.Add(1) })
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if n := runs.Load(); n < 3 || n > 10 {
		t.Fatalf("runs = %d", n)
	}
}
