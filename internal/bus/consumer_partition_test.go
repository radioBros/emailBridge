package bus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumePartitionAckAndDelayedRetry(t *testing.T) {
	b := openEmbedded(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	got := make(chan Message, 2)
	stop, err := b.ConsumePartition(ctx, StreamDelivery, "delivery-test", DeliverySubject(3), 2, func(_ context.Context, m Message) error {
		got <- m
		if calls.Add(1) == 1 {
			return DelayRetry(20*time.Millisecond, errors.New("temporary"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if _, err := b.Publish(ctx, DeliverySubject(3), []byte(`{"deliveryId":"dlv_1"}`), "dlv_1", nil); err != nil {
		t.Fatal(err)
	}
	first := waitMessage(t, got)
	second := waitMessage(t, got)
	if first.Subject != DeliverySubject(3) || string(first.Data) != `{"deliveryId":"dlv_1"}` {
		t.Fatalf("message = %+v", first)
	}
	if second.NumDelivered < 2 {
		t.Fatalf("redelivery metadata = %+v", second)
	}
}

func TestConsumePartitionValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := (*Bus)(nil).ConsumePartition(ctx, "s", "d", "x", 1, func(context.Context, Message) error { return nil }); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("nil bus: %v", err)
	}
	b := openEmbedded(t)
	for _, tc := range []struct {
		stream, durable, subject string
		handler                  func(context.Context, Message) error
	}{
		{"", "d", "x", func(context.Context, Message) error { return nil }},
		{"s", "", "x", func(context.Context, Message) error { return nil }},
		{"s", "d", "", func(context.Context, Message) error { return nil }},
		{"s", "d", "x", nil},
	} {
		if _, err := b.ConsumePartition(ctx, tc.stream, tc.durable, tc.subject, 1, tc.handler); err == nil {
			t.Fatal("invalid consumer accepted")
		}
	}
	if (&RetryAfter{}).Error() != "retry later" {
		t.Fatal("retry error text")
	}
	if (&Discard{}).Error() != "discard message" {
		t.Fatal("discard error text")
	}
	cause := errors.New("cause")
	if !errors.Is(DelayRetry(time.Second, cause), cause) || !errors.Is(DiscardMessage(cause), cause) {
		t.Fatal("consumer marker unwrap")
	}
}

func TestConsumePartitionDiscardsPoisonMessage(t *testing.T) {
	b := openEmbedded(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 2)
	stop, err := b.ConsumePartition(ctx, StreamDelivery, "delivery-discard", DeliverySubject(4), 0, func(context.Context, Message) error {
		called <- struct{}{}
		return DiscardMessage(errors.New("poison"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := b.Publish(ctx, DeliverySubject(4), []byte(`bad`), "poison-1", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("handler not called")
	}
	select {
	case <-called:
		t.Fatal("terminated message was redelivered")
	case <-time.After(100 * time.Millisecond):
	}
}

func waitMessage(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return Message{}
	}
}
