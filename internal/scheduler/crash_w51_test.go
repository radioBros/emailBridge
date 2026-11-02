package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCrash struct {
	n    atomic.Int32
	mu   sync.Mutex
	duty string
}

func (f *fakeCrash) CapturePanic(_ context.Context, _ any, _ []byte, tags map[string]string) {
	f.mu.Lock()
	f.duty = tags["duty"]
	f.mu.Unlock()
	f.n.Add(1)
}

func (f *fakeCrash) lastDuty() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.duty
}

func TestDutyPanicReported(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	crash := &fakeCrash{}
	s := &Scheduler{log: log, crash: crash, duties: map[string]Duty{}}
	ctx, cancel := context.WithCancel(context.Background())
	s.lctx, s.cancel = ctx, cancel
	s.leader = true
	var ran atomic.Int32
	s.Add("panics", func(context.Context) {
		ran.Add(1)
		panic("duty-bug")
	})
	deadline := time.Now().Add(2 * time.Second)
	for ran.Load() == 0 || crash.n.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ran=%d reports=%d", ran.Load(), crash.n.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if crash.n.Load() != 1 || crash.lastDuty() != "panics" {
		t.Fatalf("reports=%d duty=%q", crash.n.Load(), crash.lastDuty())
	}
	cancel()
	s.wg.Wait()
}
