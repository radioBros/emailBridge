package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Backlog is what a stream still has to process (W75 queue watcher).
type Backlog struct {
	Name string
	// Pending is the number of unprocessed messages: the stored messages of a work-queue stream (acked ones
	// are removed), or the consumers' lag (not yet delivered + delivered but unacknowledged) otherwise.
	Pending    int64
	AckPending int64
	// Oldest is the time the oldest unprocessed message was stored (zero when nothing is pending or unknown).
	Oldest time.Time
}

// StreamBacklog reports the backlog of stream name. For a work-queue stream its first message is the oldest
// pending one; for a retaining stream it is the first message after the slowest consumer's ack floor.
func (b *Bus) StreamBacklog(ctx context.Context, name string) (Backlog, error) {
	out := Backlog{Name: name}
	if b == nil || b.JS == nil {
		return out, ErrNotConnected
	}
	st, err := b.JS.Stream(ctx, name)
	if err != nil {
		return out, fmt.Errorf("bus: stream %s: %w", name, err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		return out, fmt.Errorf("bus: stream info %s: %w", name, err)
	}
	var lag uint64
	var slowest *jetstream.ConsumerInfo
	lister := st.ListConsumers(ctx)
	for ci := range lister.Info() {
		out.AckPending += int64(ci.NumAckPending)
		pending := ci.NumPending + uint64(max(ci.NumAckPending, 0))
		lag += pending
		if pending > 0 && (slowest == nil || ci.AckFloor.Stream < slowest.AckFloor.Stream) {
			slowest = ci
		}
	}
	if err := lister.Err(); err != nil {
		return out, fmt.Errorf("bus: consumers %s: %w", name, err)
	}
	if info.Config.Retention == jetstream.WorkQueuePolicy {
		out.Pending = clampInt64(info.State.Msgs)
		if info.State.Msgs > 0 {
			out.Oldest = info.State.FirstTime
		}
		return out, nil
	}
	out.Pending = clampInt64(lag)
	if slowest != nil {
		if msg, err := st.GetMsg(ctx, slowest.AckFloor.Stream+1); err == nil {
			out.Oldest = msg.Time
		}
	}
	return out, nil
}

func clampInt64(v uint64) int64 {
	if v > 1<<62 {
		return 1 << 62
	}
	return int64(v)
}
