package bus

import (
	"context"
	"encoding/json"
	"fmt"
)

// QueueControlSubject carries pause / resume of a work stream to every node (S4 queues).
const QueueControlSubject = "eb.queue.control"

// QueueControl is the payload of QueueControlSubject.
type QueueControl struct {
	Queue  string `json:"queue"`
	Paused bool   `json:"paused"`
}

// SetQueuePaused marks a JetStream work stream paused (consumers stop pulling) or resumed.
func (b *Bus) SetQueuePaused(queue string, paused bool) {
	if b == nil || queue == "" {
		return
	}
	b.pauseMu.Lock()
	defer b.pauseMu.Unlock()
	if b.paused == nil {
		b.paused = map[string]bool{}
	}
	if b.pauseWait == nil {
		b.pauseWait = map[string]chan struct{}{}
	}
	was := b.paused[queue]
	b.paused[queue] = paused
	if was == paused {
		return
	}
	if ch, ok := b.pauseWait[queue]; ok {
		close(ch)
		delete(b.pauseWait, queue)
	}
}

// QueuePaused reports whether consumers of queue should stop pulling.
func (b *Bus) QueuePaused(queue string) bool {
	if b == nil {
		return false
	}
	b.pauseMu.Lock()
	defer b.pauseMu.Unlock()
	return b.paused[queue]
}

// PublishQueuePaused broadcasts a pause / resume so every node updates its consumers.
func (b *Bus) PublishQueuePaused(queue string, paused bool) error {
	if b == nil || b.Conn == nil {
		return ErrNotConnected
	}
	b.SetQueuePaused(queue, paused)
	data, err := json.Marshal(QueueControl{Queue: queue, Paused: paused})
	if err != nil {
		return err
	}
	return b.PublishCore(QueueControlSubject, data)
}

// watchQueueControl applies pause broadcasts from other nodes.
func (b *Bus) watchQueueControl() (unsubscribe func() error, err error) {
	return b.SubscribeCore(QueueControlSubject, func(_ string, data []byte) {
		var c QueueControl
		if json.Unmarshal(data, &c) != nil || c.Queue == "" {
			return
		}
		b.SetQueuePaused(c.Queue, c.Paused)
	})
}

// waitWhilePaused blocks while queue is paused; returns false when ctx is done.
func (b *Bus) waitWhilePaused(ctx context.Context, queue string) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		b.pauseMu.Lock()
		if !b.paused[queue] {
			b.pauseMu.Unlock()
			return true
		}
		ch := b.pauseWait[queue]
		if ch == nil {
			ch = make(chan struct{})
			if b.pauseWait == nil {
				b.pauseWait = map[string]chan struct{}{}
			}
			b.pauseWait[queue] = ch
		}
		b.pauseMu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-ch:
		}
	}
}

// WorkStreams are the JetStream work streams listed by GET /v1/queues (S4 §1).
func WorkStreams() []string {
	return []string{StreamEvents, StreamDelivery, StreamSubmit, StreamSysmail}
}

// QueueStats is the transport-neutral view of one work stream for /v1/queues.
type QueueStats struct {
	Name       string
	Pending    uint64
	AckPending int
	Consumers  int
	Paused     bool
	Keep       string // MaxAge as S9 duration, empty when unset
}

// QueueStats returns pending / ack-pending / consumer counts for a stream.
func (b *Bus) QueueStats(ctx context.Context, name string) (QueueStats, error) {
	if b == nil || b.JS == nil {
		return QueueStats{}, ErrNotConnected
	}
	s, err := b.JS.Stream(ctx, name)
	if err != nil {
		return QueueStats{}, fmt.Errorf("bus: stream %s: %w", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return QueueStats{}, fmt.Errorf("bus: stream info %s: %w", name, err)
	}
	var ackPending int
	lister := s.ListConsumers(ctx)
	for ci := range lister.Info() {
		ackPending += ci.NumAckPending
	}
	if err := lister.Err(); err != nil {
		return QueueStats{}, fmt.Errorf("bus: consumers %s: %w", name, err)
	}
	keep := ""
	if info.Config.MaxAge > 0 {
		keep = formatKeep(info.Config.MaxAge)
	}
	return QueueStats{
		Name: name, Pending: info.State.Msgs, AckPending: ackPending,
		Consumers: info.State.Consumers, Paused: b.QueuePaused(name), Keep: keep,
	}, nil
}

// SetStreamMaxAge updates a stream's MaxAge (finished-job / message retention window).
func (b *Bus) SetStreamMaxAge(ctx context.Context, name string, keep string) error {
	if b == nil || b.JS == nil {
		return ErrNotConnected
	}
	d, err := parseKeep(keep)
	if err != nil {
		return err
	}
	s, err := b.JS.Stream(ctx, name)
	if err != nil {
		return fmt.Errorf("bus: stream %s: %w", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("bus: stream info %s: %w", name, err)
	}
	cfg := info.Config
	cfg.MaxAge = d
	if _, err := b.JS.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("bus: update stream %s: %w", name, err)
	}
	return nil
}
