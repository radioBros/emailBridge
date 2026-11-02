package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// TestStreamBacklog: a work-queue stream reports its stored messages and the first one's time; a retaining
// stream reports the consumers' lag and the time of the first message the slowest consumer has not acked.
func TestStreamBacklog(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()
	empty, err := b.StreamBacklog(ctx, StreamSysmail)
	if err != nil || empty.Pending != 0 || !empty.Oldest.IsZero() {
		t.Fatalf("empty work queue: %+v %v", empty, err)
	}
	before := time.Now().Add(-time.Second)
	if _, err := b.Publish(ctx, SysmailSubject, []byte(`{}`), "s1", nil); err != nil {
		t.Fatal(err)
	}
	wq, err := b.StreamBacklog(ctx, StreamSysmail)
	if err != nil || wq.Pending != 1 || wq.Oldest.Before(before) {
		t.Fatalf("work queue: %+v %v", wq, err)
	}

	if _, err := b.Publish(ctx, "eb.evt.backlog.test", []byte(`{}`), "e1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.JS.CreateOrUpdateConsumer(ctx, StreamEvents, jetstream.ConsumerConfig{
		Durable: "backlog-test", AckPolicy: jetstream.AckExplicitPolicy, FilterSubject: "eb.evt.backlog.>",
	}); err != nil {
		t.Fatal(err)
	}
	ev, err := b.StreamBacklog(ctx, StreamEvents)
	if err != nil || ev.Pending != 1 || ev.Oldest.Before(before) {
		t.Fatalf("retaining stream: %+v %v", ev, err)
	}

	if _, err := b.StreamBacklog(ctx, "NOPE"); err == nil {
		t.Fatal("unknown stream must fail")
	}
	var nb *Bus
	if _, err := nb.StreamBacklog(ctx, StreamEvents); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("nil bus: %v", err)
	}
	if clampInt64(1<<63) != 1<<62 || clampInt64(5) != 5 {
		t.Fatal("clamp")
	}
}
