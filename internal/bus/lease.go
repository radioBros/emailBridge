package bus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Lease errors.
var (
	// ErrLeaseHeld: another holder owns a live lease.
	ErrLeaseHeld = errors.New("bus: lease held by another node")
	// ErrLeaseLost: the renewal or release did not match our epoch (the lease expired and was taken).
	ErrLeaseLost = errors.New("bus: lease lost")
)

// LeaseInfo is the current holder of a lease key.
type LeaseInfo struct {
	Value []byte
	Epoch uint64 // KV revision of the latest write
}

// Claim creates the lease key with a per-key TTL. It succeeds only if the key does not exist; the
// returned revision is the lease epoch (S4 §5).
func (b *Bus) Claim(ctx context.Context, key string, value []byte, ttl time.Duration) (uint64, error) {
	rev, err := b.leases.Create(ctx, key, value, jetstream.KeyTTL(ttl))
	if errors.Is(err, jetstream.ErrKeyExists) {
		return 0, ErrLeaseHeld
	}
	if err != nil {
		return 0, fmt.Errorf("bus: claim %s: %w", key, err)
	}
	return rev, nil
}

// Renew extends the lease by ttl if epoch is still the latest revision of the key. NATS KV cannot set a
// TTL on Update, so the renewal publishes to the key's subject with an expected-last-subject-sequence
// check and a Nats-TTL header (verified in SP4). It returns the new epoch.
func (b *Bus) Renew(ctx context.Context, key string, value []byte, epoch uint64, ttl time.Duration) (uint64, error) {
	m := &nats.Msg{Subject: "$KV." + BucketLeases + "." + key, Data: value, Header: nats.Header{}}
	ack, err := b.JS.PublishMsg(ctx, m, jetstream.WithExpectLastSequencePerSubject(epoch), jetstream.WithMsgTTL(ttl))
	if err != nil {
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence || apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant) {
			return 0, ErrLeaseLost
		}
		return 0, fmt.Errorf("bus: renew %s: %w", key, err)
	}
	return ack.Sequence, nil
}

// Release deletes the lease if epoch is still current.
func (b *Bus) Release(ctx context.Context, key string, epoch uint64) error {
	err := b.leases.Delete(ctx, key, jetstream.LastRevision(epoch))
	if err != nil {
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence || apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant) {
			return ErrLeaseLost
		}
		return fmt.Errorf("bus: release %s: %w", key, err)
	}
	return nil
}

// LeaseHolder returns the current holder of key, or (nil, nil) when the lease is free.
func (b *Bus) LeaseHolder(ctx context.Context, key string) (*LeaseInfo, error) {
	e, err := b.leases.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bus: lease %s: %w", key, err)
	}
	return &LeaseInfo{Value: e.Value(), Epoch: e.Revision()}, nil
}
