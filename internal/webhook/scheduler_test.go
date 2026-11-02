package webhook

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerStrictSerializesPerAccount(t *testing.T) {
	s := newScheduler()
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.run(context.Background(), "end_1", "acc_1", OrderingStrict, 8, func() error {
				current := active.Add(1)
				peak.CompareAndSwap(peak.Load(), current)
				time.Sleep(time.Millisecond)
				active.Add(-1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 1 || len(s.keys) != 0 {
		t.Fatalf("peak=%d keys=%d", peak.Load(), len(s.keys))
	}
}

func TestSchedulerConcurrencyAndAccountIsolation(t *testing.T) {
	s := newScheduler()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for _, accountID := range []string{"acc_1", "acc_2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.run(context.Background(), "end_1", accountID, OrderingStrict, 2, func() error {
				started <- struct{}{}
				<-release
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("accounts were not isolated")
		}
	}
	close(release)
	wg.Wait()
}

func TestSchedulerCancellationAndQueueLimit(t *testing.T) {
	s := newScheduler()
	slots := s.endpoint("end_1", 1)
	slots.sem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.run(ctx, "end_1", "acc_1", OrderingBestEffort, 1, func() error {
		t.Fatal("work ran")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	<-slots.sem

	key := &orderingKey{waiting: maxOrderingQueue}
	s.keys["end_1/acc_1"] = key
	if _, ok := s.enterKey("end_1/acc_1"); ok {
		t.Fatal("full queue accepted")
	}
}
