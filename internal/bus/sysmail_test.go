package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestConsumeSysmailAck(t *testing.T) {
	b := openEmbedded(t)
	ctx := context.Background()

	got := make(chan []byte, 1)
	stop, err := b.ConsumeSysmail(ctx, func(_ context.Context, data []byte) error {
		got <- append([]byte(nil), data...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)

	cons, err := b.JS.Consumer(ctx, StreamSysmail, "sysmail")
	if err != nil {
		t.Fatal(err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.MaxDeliver != 5 || info.Config.AckPolicy != jetstream.AckExplicitPolicy {
		t.Fatalf("consumer config %+v", info.Config)
	}
	if len(info.Config.BackOff) == 0 || info.Config.BackOff[0] != 30*time.Second {
		t.Fatalf("backoff %+v", info.Config.BackOff)
	}

	if _, err := b.Publish(ctx, SysmailSubject, []byte(`{"kind":"alert"}`), "job_test1", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-got:
		if string(data) != `{"kind":"alert"}` {
			t.Fatalf("payload %s", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
}

func TestConsumeSysmailNilBus(t *testing.T) {
	_, err := (*Bus)(nil).ConsumeSysmail(context.Background(), func(context.Context, []byte) error { return nil })
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
}
