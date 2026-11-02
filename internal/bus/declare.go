package bus

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Stream names (S4 §4).
const (
	StreamEvents   = "EMAILBRIDGE_EVENTS"
	StreamDelivery = "EMAILBRIDGE_DELIVERY"
	StreamSubmit   = "EMAILBRIDGE_SUBMIT"
	StreamSysmail  = "EMAILBRIDGE_SYSMAIL"
)

// KV bucket names (S4 §5).
const (
	BucketNodes  = "eb-nodes"
	BucketLeases = "eb-leases"
	BucketCache  = "eb-cache"
)

// DedupWindow is the JetStream duplicate window of every stream (S4 §4).
const DedupWindow = 10 * time.Minute

// NodeHeartbeat is the default node-registry heartbeat; the eb-nodes bucket expires entries after three.
const NodeHeartbeat = 5 * time.Second

func (b *Bus) streamConfigs() []jetstream.StreamConfig {
	r := b.o.Replicas
	return []jetstream.StreamConfig{
		{Name: StreamEvents, Subjects: []string{"eb.evt.>"}, Retention: jetstream.LimitsPolicy, Storage: jetstream.FileStorage,
			MaxAge: 72 * time.Hour, Duplicates: DedupWindow, Replicas: r, Discard: jetstream.DiscardOld},
		{Name: StreamDelivery, Subjects: []string{"eb.dlv.>"}, Retention: jetstream.WorkQueuePolicy, Storage: jetstream.FileStorage,
			MaxAge: 7 * 24 * time.Hour, Duplicates: DedupWindow, Replicas: r},
		{Name: StreamSubmit, Subjects: []string{"eb.submit.>"}, Retention: jetstream.WorkQueuePolicy, Storage: jetstream.FileStorage,
			MaxAge: 7 * 24 * time.Hour, Duplicates: DedupWindow, Replicas: r},
		{Name: StreamSysmail, Subjects: []string{"eb.sysmail"}, Retention: jetstream.WorkQueuePolicy, Storage: jetstream.FileStorage,
			MaxAge: 24 * time.Hour, Duplicates: DedupWindow, Replicas: r},
	}
}

// declare creates or updates every stream and bucket; safe to run on every start of every node.
func (b *Bus) declare(ctx context.Context) error {
	for _, sc := range b.streamConfigs() {
		if _, err := b.JS.CreateOrUpdateStream(ctx, sc); err != nil {
			return fmt.Errorf("bus: stream %s: %w", sc.Name, err)
		}
	}
	var err error
	if b.nodes, err = b.JS.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: BucketNodes, History: 1,
		TTL: 3 * NodeHeartbeat, Storage: jetstream.MemoryStorage, Replicas: b.o.Replicas}); err != nil {
		return fmt.Errorf("bus: bucket %s: %w", BucketNodes, err)
	}
	// Leases: per-key TTL on create and on every renewal (S4 §5, SP4 design B).
	if b.leases, err = b.JS.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: BucketLeases, History: 1,
		LimitMarkerTTL: 2 * time.Second, Storage: jetstream.FileStorage, Replicas: b.o.Replicas}); err != nil {
		return fmt.Errorf("bus: bucket %s: %w", BucketLeases, err)
	}
	if b.cache, err = b.JS.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: BucketCache, History: 1,
		LimitMarkerTTL: 2 * time.Second, Storage: jetstream.MemoryStorage, Replicas: b.o.Replicas}); err != nil {
		return fmt.Errorf("bus: bucket %s: %w", BucketCache, err)
	}
	return nil
}

// NodesBucket returns the node-registry KV bucket.
func (b *Bus) NodesBucket() jetstream.KeyValue { return b.nodes }

// Cache returns the short-lived cache bucket (Limen cache store, rate-limit counters, S10 §2.1).
func (b *Bus) Cache() jetstream.KeyValue { return b.cache }

func splitHostPort(hp string) (string, int, error) {
	host, ps, err := net.SplitHostPort(hp)
	if err != nil {
		return "", 0, fmt.Errorf("bus: listen address %q: %w", hp, err)
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("bus: listen address %q: bad port", hp)
	}
	return host, port, nil
}
