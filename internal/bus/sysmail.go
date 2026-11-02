package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// sysmailBackoff is MaxDeliver 5 over ~15 min (S10 §7): 30s → 5min.
var sysmailBackoff = []time.Duration{
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
}

// ConsumeSysmail starts a durable pull consumer "sysmail" on EMAILBRIDGE_SYSMAIL with explicit ack,
// MaxDeliver 5 and backoff 30s→5min. handler errors Nak with delay; nil acks. The returned stop
// function ends consumption.
func (b *Bus) ConsumeSysmail(ctx context.Context, handler func(ctx context.Context, data []byte) error) (stop func(), err error) {
	if b == nil || b.JS == nil {
		return nil, ErrNotConnected
	}
	cons, err := b.JS.CreateOrUpdateConsumer(ctx, StreamSysmail, jetstream.ConsumerConfig{
		Durable:       "sysmail",
		FilterSubject: SysmailSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    5,
		AckWait:       30 * time.Second,
		BackOff:       sysmailBackoff,
	})
	if err != nil {
		return nil, fmt.Errorf("bus: sysmail consumer: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		wg.Add(1)
		defer wg.Done()
		if err := handler(runCtx, msg.Data()); err != nil {
			d := sysmailBackoff[len(sysmailBackoff)-1]
			if meta, merr := msg.Metadata(); merr == nil {
				i := int(meta.NumDelivered) - 1 //nolint:gosec // G115: delivery count is small
				if i >= 0 && i < len(sysmailBackoff) {
					d = sysmailBackoff[i]
				}
			}
			_ = msg.NakWithDelay(d)
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bus: sysmail consume: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			cc.Stop()
			wg.Wait()
		})
	}, nil
}
