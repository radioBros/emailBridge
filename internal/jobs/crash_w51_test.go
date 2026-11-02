package jobs

import (
	"context"
	"sync/atomic"
	"testing"
)

type fakeCrash struct {
	n     atomic.Int32
	last  any
	tags  map[string]string
	stack []byte
}

func (f *fakeCrash) CapturePanic(_ context.Context, recovered any, stack []byte, tags map[string]string) {
	f.n.Add(1)
	f.last = recovered
	f.stack = append([]byte(nil), stack...)
	f.tags = tags
}

func TestRunSafelyReportsPanicOnce(t *testing.T) {
	crash := &fakeCrash{}
	s := &Service{CrashReporter: crash, NodeID: "n1"}
	j := &Job{ID: "job_01jbf3k8v4w7q2m9x5c6t0r1yz", Kind: "demo.panic"}
	_, err := s.runSafely(context.Background(), func(context.Context, *Job, Reporter) (any, error) {
		panic("job-boom")
	}, j, &progressRep{})
	if err == nil {
		t.Fatal("expected error")
	}
	if crash.n.Load() != 1 {
		t.Fatalf("reports=%d", crash.n.Load())
	}
	if crash.last != "job-boom" || crash.tags["component"] != "jobs" || crash.tags["kind"] != "demo.panic" {
		t.Fatalf("%v %v", crash.last, crash.tags)
	}
	if len(crash.stack) == 0 {
		t.Fatal("missing stack")
	}
	if _, err := s.runSafely(context.Background(), func(context.Context, *Job, Reporter) (any, error) {
		return "ok", nil
	}, j, &progressRep{}); err != nil {
		t.Fatal(err)
	}
	if crash.n.Load() != 1 {
		t.Fatalf("success must not report: %d", crash.n.Load())
	}
}

type progressRep struct{ cancelled atomic.Bool }

func (p *progressRep) Progress(context.Context, int64, int64) {}
func (p *progressRep) Cancelled() bool                        { return p.cancelled.Load() }
