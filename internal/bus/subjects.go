package bus

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Partition returns hash(key) mod n with FNV-1a 64 (S4 §2). n must be > 0.
func Partition(key string, n int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(n)) //nolint:gosec // G115: n is a small positive partition count
}

// EventPartition is the event-stream partition of an account (events without an account use 0).
func EventPartition(accountID string, n int) int {
	if accountID == "" {
		return 0
	}
	return Partition(accountID, n)
}

// DeliveryPartition is the delivery partition of an (endpoint, account) pair.
func DeliveryPartition(endpointID, accountID string, n int) int {
	return Partition(endpointID+"/"+accountID, n)
}

// EventSubject is eb.evt.<p>.<orgId>.<type tokens> (S4 §2); dots in the type become subject tokens.
func EventSubject(p int, orgID, eventType string) string {
	return fmt.Sprintf("eb.evt.%d.%s.%s", p, orgID, eventType)
}

// DeliverySubject is eb.dlv.<p>.
func DeliverySubject(p int) string { return fmt.Sprintf("eb.dlv.%d", p) }

// SubmitSubject is eb.submit.<accountId>.
func SubmitSubject(accountID string) string { return "eb.submit." + accountID }

// SysmailSubject is the system-mail work queue subject.
const SysmailSubject = "eb.sysmail"

// NodeSyncSubject is eb.node.<nodeId>.sync (sync commands, S4 §6).
func NodeSyncSubject(nodeID string) string { return "eb.node." + nodeID + ".sync" }

// ClusterDrainSubject is eb.cluster.drain.<nodeId>: a message on it starts draining that node (S4 §6).
func ClusterDrainSubject(nodeID string) string { return "eb.cluster.drain." + nodeID }

// SettingsChangedSubject announces runtime settings changes.
const SettingsChangedSubject = "eb.settings.changed"

// RealtimeSubject is eb.rt.<orgId>.<topic> (admin WebSocket fan-in, S13 §6).
func RealtimeSubject(orgID, topic string) string { return "eb.rt." + orgID + "." + topic }

// LogsQuerySubject is the scatter-gather subject of cluster log queries (S12 §8).
const LogsQuerySubject = "eb.logs.query"

// ValidToken reports whether s can be used as one subject token (IDs never contain dots, S4 §2).
func ValidToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ". *>\t\r\n")
}

// ErrBadToken is returned when an ID would break the subject layout.
var ErrBadToken = errors.New("bus: value is not a valid subject token")

// Publish publishes data with Nats-Msg-Id = msgID (JetStream dedup, S4 §3) and waits for the ack.
// It returns whether the stream reported the message as a duplicate.
func (b *Bus) Publish(ctx context.Context, subject string, data []byte, msgID string, headers map[string]string) (duplicate bool, err error) {
	m := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	for k, v := range headers {
		m.Header.Set(k, v)
	}
	var opts []jetstream.PublishOpt
	if msgID != "" {
		opts = append(opts, jetstream.WithMsgID(msgID))
	}
	ack, err := b.JS.PublishMsg(ctx, m, opts...)
	if err != nil {
		return false, fmt.Errorf("bus: publish %s: %w", subject, err)
	}
	return ack.Duplicate, nil
}
