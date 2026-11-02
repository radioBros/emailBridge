package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Message is the transport-neutral part of a JetStream message exposed to domain consumers.
type Message struct {
	Subject      string
	Data         []byte
	NumDelivered uint64
}

// RetryAfter marks a handler error for delayed JetStream redelivery.
type RetryAfter struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfter) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "retry later"
}

// Unwrap returns the cause of a delayed retry.
func (e *RetryAfter) Unwrap() error { return e.Err }

// DelayRetry returns an error that makes ConsumePartition NAK the message with delay.
func DelayRetry(delay time.Duration, err error) error {
	return &RetryAfter{Delay: delay, Err: err}
}

// Discard marks a poison message for termination instead of redelivery.
type Discard struct {
	Err error
}

func (e *Discard) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "discard message"
}

// Unwrap returns the poison-message cause.
func (e *Discard) Unwrap() error { return e.Err }

// DiscardMessage returns an error that makes ConsumePartition terminate the message.
func DiscardMessage(err error) error { return &Discard{Err: err} }

// ConsumePartition creates or updates one explicit-ack durable pull consumer and runs h concurrently up
// to maxAckPending. A nil handler result ACKs, an error NAKs, and RetryAfter NAKs with its delay.
// When the stream is paused (SetQueuePaused), pulling stops until the queue is resumed.
func (b *Bus) ConsumePartition(
	ctx context.Context,
	stream, durable, subject string,
	maxAckPending int,
	h func(context.Context, Message) error,
) (stop func(), err error) {
	if b == nil || b.JS == nil {
		return nil, ErrNotConnected
	}
	if stream == "" || durable == "" || subject == "" || h == nil {
		return nil, errors.New("bus: stream, durable, subject and handler are required")
	}
	if maxAckPending <= 0 {
		maxAckPending = 1
	}
	consumer, err := b.JS.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxAckPending: maxAckPending,
	})
	if err != nil {
		return nil, fmt.Errorf("bus: consumer %s: %w", durable, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			cancel()
			wg.Wait()
		})
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for runCtx.Err() == nil {
			if !b.waitWhilePaused(runCtx, stream) {
				return
			}
			b.runConsumeSession(runCtx, &wg, consumer, stream, maxAckPending, h)
		}
	}()
	return stopFn, nil
}

func (b *Bus) runConsumeSession(
	ctx context.Context,
	parentWG *sync.WaitGroup,
	consumer jetstream.Consumer,
	stream string,
	maxAckPending int,
	h func(context.Context, Message) error,
) {
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				if b.QueuePaused(stream) {
					sessionCancel()
					return
				}
			}
		}
	}()

	sem := make(chan struct{}, maxAckPending)
	var inFlight sync.WaitGroup
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		if sessionCtx.Err() != nil || b.QueuePaused(stream) {
			_ = msg.Nak()
			return
		}
		select {
		case sem <- struct{}{}:
		case <-sessionCtx.Done():
			_ = msg.Nak()
			return
		}
		inFlight.Add(1)
		parentWG.Add(1)
		go func() {
			defer func() {
				<-sem
				inFlight.Done()
				parentWG.Done()
			}()
			heartbeatDone := make(chan struct{})
			defer close(heartbeatDone)
			go func() {
				ticker := time.NewTicker(20 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						_ = msg.InProgress()
					case <-heartbeatDone:
						return
					}
				}
			}()
			meta, _ := msg.Metadata()
			m := Message{Subject: msg.Subject(), Data: append([]byte(nil), msg.Data()...)}
			if meta != nil {
				m.NumDelivered = meta.NumDelivered
			}
			err := h(sessionCtx, m)
			if err == nil {
				_ = msg.Ack()
				return
			}
			var delayed *RetryAfter
			if errors.As(err, &delayed) && delayed.Delay > 0 {
				_ = msg.NakWithDelay(delayed.Delay)
				return
			}
			var discard *Discard
			if errors.As(err, &discard) {
				_ = msg.Term()
				return
			}
			_ = msg.Nak()
		}()
	})
	if err != nil {
		return
	}
	<-sessionCtx.Done()
	cc.Stop()
	inFlight.Wait()
}
