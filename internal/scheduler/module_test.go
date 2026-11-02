package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
)

func TestModuleRunsDutiesOnLeader(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleScheduler: true}, NodeID: "n1", Log: log})
	app.Provide(a, b)
	m := &Module{TTL: 3 * time.Second}
	if m.Name() != "scheduler" || m.Roles() != nil {
		t.Fatal("identity")
	}
	ctx := context.Background()
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	s, err := app.MustGet[*Scheduler](a)
	if err != nil {
		t.Fatal(err)
	}
	var early, late, panicked atomic.Int32
	s.Add("early", func(ctx context.Context) { early.Add(1); <-ctx.Done() })
	s.Add("panics", func(context.Context) { panicked.Add(1); panic("duty bug") })
	deadline := time.Now().Add(10 * time.Second)
	for !s.Leader() {
		if time.Now().After(deadline) {
			t.Fatal("never became leader")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.Add("late", func(ctx context.Context) { late.Add(1); <-ctx.Done() })
	for early.Load() == 0 || late.Load() == 0 || panicked.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("duties not started: early=%d late=%d panicked=%d", early.Load(), late.Load(), panicked.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Leader() {
		t.Fatal("still leader after stop")
	}
	if h, _ := b.LeaseHolder(ctx, LeaderKey); h != nil {
		t.Fatal("leadership not released after the duties stopped")
	}
}

func TestModuleWithoutSchedulerRole(t *testing.T) {
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n2", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	m := &Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	s, _ := app.MustGet[*Scheduler](a)
	s.Add("x", func(context.Context) { t.Error("must not run without the scheduler role") })
	time.Sleep(100 * time.Millisecond)
	if s.Leader() || m.Stop(context.Background()) != nil {
		t.Fatal("non-scheduler node")
	}
}
