package bus

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// PublishCore publishes a non-persistent message (realtime fan-out, settings notifications). Delivery is
// best effort by design: consumers refetch after reconnecting (S13 §6).
func (b *Bus) PublishCore(subject string, data []byte) error {
	if err := b.Conn.Publish(subject, data); err != nil {
		return fmt.Errorf("bus: publish %s: %w", subject, err)
	}
	return nil
}

// SubscribeCore subscribes to a core subject (wildcards allowed). The handler gets the concrete subject
// and payload; it runs on the connection's dispatcher, so it must be quick.
func (b *Bus) SubscribeCore(subject string, h func(subject string, data []byte)) (unsubscribe func() error, err error) {
	sub, err := b.Conn.Subscribe(subject, func(m *nats.Msg) { h(m.Subject, m.Data) })
	if err != nil {
		return nil, fmt.Errorf("bus: subscribe %s: %w", subject, err)
	}
	return sub.Unsubscribe, nil
}

// SubscribeCoreReply is SubscribeCore for request-reply subjects: when the handler returns a non-nil reply
// and the message expects one, the reply is sent back. A plain publish gets no reply.
func (b *Bus) SubscribeCoreReply(subject string, h func(subject string, data []byte) []byte) (unsubscribe func() error, err error) {
	sub, err := b.Conn.Subscribe(subject, func(m *nats.Msg) {
		if out := h(m.Subject, m.Data); out != nil && m.Reply != "" {
			_ = m.Respond(out)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("bus: subscribe %s: %w", subject, err)
	}
	return sub.Unsubscribe, nil
}

// RequestCore sends a core request and waits for one reply until ctx ends.
func (b *Bus) RequestCore(ctx context.Context, subject string, data []byte) ([]byte, error) {
	msg, err := b.Conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("bus: request %s: %w", subject, err)
	}
	return msg.Data, nil
}

// SubscribeStream delivers the messages of a JetStream stream (optionally filtered by subject) to h
// through an ephemeral ordered consumer: from the start when all is true, else only new messages. Used for
// fan-in that tolerates gaps (realtime, S4 §4 rt-<nodeId>) and by tests.
func (b *Bus) SubscribeStream(ctx context.Context, stream, filter string, all bool, h func(subject string, data []byte)) (stop func(), err error) {
	if b == nil || b.JS == nil {
		return nil, ErrNotConnected
	}
	cfg := jetstream.OrderedConsumerConfig{DeliverPolicy: jetstream.DeliverNewPolicy}
	if all {
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	}
	if filter != "" {
		cfg.FilterSubjects = []string{filter}
	}
	cons, err := b.JS.OrderedConsumer(ctx, stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("bus: subscribe %s: %w", stream, err)
	}
	cc, err := cons.Consume(func(m jetstream.Msg) { h(m.Subject(), m.Data()) })
	if err != nil {
		return nil, fmt.Errorf("bus: consume %s: %w", stream, err)
	}
	return cc.Stop, nil
}
