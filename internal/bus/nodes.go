package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// NodeInfo is a node's entry in the eb-nodes registry (S4 §5). Entries expire three heartbeats after the
// node stops refreshing them.
type NodeInfo struct {
	NodeID    string    `json:"nodeId"`
	Roles     []string  `json:"roles"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	SeenAt    time.Time `json:"seenAt"`
	Accounts  int       `json:"accounts"`
	// Partitions maps lease prefix (e.g. "evt", "dlv", "relay") to the number of partitions this node holds.
	Partitions map[string]int `json:"partitions,omitempty"`
	// Draining nodes get no new keys; placement moves their keys away through hand-offs (S4 §6).
	Draining bool `json:"draining,omitempty"`
}

// LoadProvider returns the account count and held partition counts for this node's heartbeat (S4 §5).
type LoadProvider func() (accounts int, partitions map[string]int)

// SetLoadProvider registers the function that fills Accounts and Partitions on every heartbeat.
func (b *Bus) SetLoadProvider(p LoadProvider) {
	if b == nil {
		return
	}
	b.loadMu.Lock()
	b.load = p
	b.loadMu.Unlock()
}

// SetAccountsProvider sets only the account side of the load (sync manager); partition counts stay
// whatever balancers reported through SetPartitionLoad.
func (b *Bus) SetAccountsProvider(fn func() int) {
	if b == nil {
		return
	}
	b.loadMu.Lock()
	b.accountsFn = fn
	b.loadMu.Unlock()
}

// SetPartitionLoad records how many leases of prefix this node holds (called from Balancer.OnChange).
func (b *Bus) SetPartitionLoad(prefix string, n int) {
	if b == nil || prefix == "" {
		return
	}
	b.loadMu.Lock()
	if b.partLoad == nil {
		b.partLoad = map[string]int{}
	}
	if n <= 0 {
		delete(b.partLoad, prefix)
	} else {
		b.partLoad[prefix] = n
	}
	b.loadMu.Unlock()
}

func (b *Bus) currentLoad() (accounts int, partitions map[string]int) {
	b.loadMu.Lock()
	defer b.loadMu.Unlock()
	if b.load != nil {
		accounts, partitions = b.load()
		return accounts, clonePartMap(partitions)
	}
	if b.accountsFn != nil {
		accounts = b.accountsFn()
	}
	partitions = clonePartMap(b.partLoad)
	return accounts, partitions
}

func clonePartMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// RegisterNode writes (or refreshes) this node's registry entry.
func (b *Bus) RegisterNode(ctx context.Context, info NodeInfo) error {
	if b == nil || b.nodes == nil {
		return ErrNotConnected
	}
	info.SeenAt = time.Now().UTC()
	if info.NodeID == b.o.NodeID {
		info.Draining = b.draining.Load()
		acc, parts := b.currentLoad()
		info.Accounts = acc
		info.Partitions = parts
		b.selfMu.Lock()
		cp := info
		b.self = &cp
		b.selfMu.Unlock()
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if _, err := b.nodes.Put(ctx, info.NodeID, data); err != nil {
		return fmt.Errorf("bus: register node: %w", err)
	}
	return nil
}

// SetDraining marks this node draining (or not) in eb-nodes right away; every later heartbeat keeps the flag.
func (b *Bus) SetDraining(ctx context.Context, on bool) error {
	if b == nil || b.nodes == nil {
		return ErrNotConnected
	}
	b.draining.Store(on)
	b.selfMu.Lock()
	info := NodeInfo{NodeID: b.o.NodeID}
	if b.self != nil {
		info = *b.self
	}
	b.selfMu.Unlock()
	return b.RegisterNode(ctx, info)
}

// Draining reports whether this node is draining.
func (b *Bus) Draining() bool { return b != nil && b.draining.Load() }

// DeregisterNode removes the entry at shutdown so partitions move without waiting for expiry.
func (b *Bus) DeregisterNode(ctx context.Context, nodeID string) error {
	if b == nil || b.nodes == nil {
		return ErrNotConnected
	}
	if err := b.nodes.Delete(ctx, nodeID); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf("bus: deregister node: %w", err)
	}
	return nil
}

// LiveNodes returns the registry entries of live nodes.
func (b *Bus) LiveNodes(ctx context.Context) ([]NodeInfo, error) {
	ids, err := b.NodeIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeInfo, 0, len(ids))
	for _, id := range ids {
		e, err := b.nodes.Get(ctx, id)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("bus: node %s: %w", id, err)
		}
		var n NodeInfo
		if json.Unmarshal(e.Value(), &n) == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// heartbeat refreshes the registry entry every interval until ctx ends, then deregisters.
func (b *Bus) heartbeat(ctx context.Context, info NodeInfo, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = b.DeregisterNode(dctx, info.NodeID)
			cancel()
			return
		case <-t.C:
		}
		hctx, cancel := context.WithTimeout(ctx, interval)
		err := b.RegisterNode(hctx, info)
		cancel()
		switch {
		case err != nil && !failing && ctx.Err() == nil:
			failing = true
			log.Warn("node heartbeat failing; other nodes will take over this node's partitions", "err", err)
		case err == nil && failing:
			failing = false
			log.Info("node heartbeat restored")
		}
	}
}
