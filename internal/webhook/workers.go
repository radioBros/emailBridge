package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
)

// RouterWork is bus.Balancer.Work for evt.<partition>.
func (s *Service) RouterWork(b *bus.Bus) func(context.Context, int, uint64) {
	return func(ctx context.Context, partition int, _ uint64) {
		durable := fmt.Sprintf("router-%d", partition)
		subject := fmt.Sprintf("eb.evt.%d.>", partition)
		s.consumeUntilStopped(ctx, func() (func(), error) {
			return b.ConsumePartition(ctx, bus.StreamEvents, durable, subject, 1, func(handlerCtx context.Context, message bus.Message) error {
				var event events.Event
				if err := json.Unmarshal(message.Data, &event); err != nil || event.ID == "" || event.EBOrg == "" {
					if err == nil {
						err = fmt.Errorf("webhook: malformed event envelope")
					}
					s.log().ErrorContext(handlerCtx, "discarding malformed event message", "subject", message.Subject, "err", err)
					return bus.DiscardMessage(err)
				}
				return s.RouteEvent(handlerCtx, event)
			})
		})
	}
}

// DeliveryWork is bus.Balancer.Work for dlv.<partition>.
func (s *Service) DeliveryWork(b *bus.Bus) func(context.Context, int, uint64) {
	return func(ctx context.Context, partition int, _ uint64) {
		durable := fmt.Sprintf("delivery-%d", partition)
		subject := bus.DeliverySubject(partition)
		s.consumeUntilStopped(ctx, func() (func(), error) {
			return b.ConsumePartition(ctx, bus.StreamDelivery, durable, subject, s.workerAckPending(), func(handlerCtx context.Context, message bus.Message) error {
				job, err := decodeJob(message.Data)
				if err != nil {
					s.log().ErrorContext(handlerCtx, "discarding malformed delivery message", "subject", message.Subject, "err", err)
					return bus.DiscardMessage(err)
				}
				return s.HandleDelivery(handlerCtx, job.DeliveryID, message.NumDelivered > 1)
			})
		})
	}
}

func (s *Service) workerAckPending() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	return 64
}

func (s *Service) consumeUntilStopped(ctx context.Context, start func() (func(), error)) {
	for ctx.Err() == nil {
		stop, err := start()
		if err == nil {
			<-ctx.Done()
			stop()
			return
		}
		s.log().ErrorContext(ctx, "webhook consumer start failed", "err", err)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
