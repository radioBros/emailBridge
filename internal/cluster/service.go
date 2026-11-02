package cluster

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// QueuesDocID is the global settings document that stores per-queue pause / keep.
const QueuesDocID = "queues"

// Service backs the cluster, stats and queues APIs.
type Service struct {
	DB     *store.DB
	Bus    *bus.Bus
	Audit  *audit.Writer
	Log    *slog.Logger
	NodeID string
	// Retention returns the KPI source retention (W74); nil uses DefaultRetention.
	Retention func() Retention
}

// ClusterView is GET /v1/cluster.
type ClusterView struct {
	Nodes     []NodeView `json:"nodes" doc:"Live nodes (heartbeat within the registry TTL)."`
	Totals    Totals     `json:"totals" doc:"Load summed over the live nodes."`
	Leader    string     `json:"leader" doc:"Node ID of the scheduler leader; empty when none is elected." example:"eb-node-1"` // scheduler leader node id, empty when none
	NodeCount int        `json:"nodeCount" doc:"Number of live nodes." example:"3"`
}

// NodeView is one eb-nodes entry for the API.
type NodeView struct {
	ID         string         `json:"id" doc:"Node ID (EB_NODE_ID or the host name)." example:"eb-node-2"`
	Roles      []string       `json:"roles" doc:"Roles the node runs (api, sync, submit, webhook, scheduler)." example:"[\"api\",\"sync\",\"webhook\"]"`
	Version    string         `json:"version" doc:"EmailBridge version of the node." example:"1.0.0"`
	StartedAt  time.Time      `json:"startedAt" doc:"When the node started." example:"2026-10-27T06:00:12Z"`
	SeenAt     time.Time      `json:"seenAt" doc:"Last heartbeat." example:"2026-10-27T10:04:55Z"`
	Draining   bool           `json:"draining" doc:"True after POST /v1/cluster/nodes/{nodeId}/drain." example:"false"`
	Accounts   int            `json:"accounts" doc:"Mail accounts the node currently syncs." example:"412"`
	Partitions map[string]int `json:"partitions,omitempty" doc:"Lease partitions held, by lease prefix (evt, dlv, relay …)." example:"{\"evt\":8,\"dlv\":8}"`
}

// Totals aggregates load across live nodes.
type Totals struct {
	Accounts   int            `json:"accounts" doc:"Accounts synced across all nodes." example:"1236"`
	Partitions map[string]int `json:"partitions,omitempty" doc:"Lease partitions held across all nodes, by prefix." example:"{\"evt\":24,\"dlv\":24}"`
}

// Cluster returns every live node, totals and the scheduler leader.
func (s *Service) Cluster(ctx context.Context) (*ClusterView, error) {
	nodes, err := s.Bus.LiveNodes(ctx)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read the node registry.").WithSafe("error", err.Error())
	}
	out := &ClusterView{Nodes: make([]NodeView, 0, len(nodes)), Totals: Totals{Partitions: map[string]int{}}}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, NodeView{
			ID: n.NodeID, Roles: n.Roles, Version: n.Version, StartedAt: n.StartedAt, SeenAt: n.SeenAt,
			Draining: n.Draining, Accounts: n.Accounts, Partitions: n.Partitions,
		})
		out.Totals.Accounts += n.Accounts
		for p, c := range n.Partitions {
			out.Totals.Partitions[p] += c
		}
	}
	out.NodeCount = len(out.Nodes)
	if len(out.Totals.Partitions) == 0 {
		out.Totals.Partitions = nil
	}
	if h, err := s.Bus.LeaseHolder(ctx, scheduler.LeaderKey); err == nil && h != nil {
		out.Leader = string(h.Value)
	}
	return out, nil
}

// Drain publishes eb.cluster.drain.<nodeId> for a live node (202).
func (s *Service) Drain(ctx context.Context, nodeID string) error {
	nodes, err := s.Bus.LiveNodes(ctx)
	if err != nil {
		return errs.New(errs.CodeInternalError, "Could not read the node registry.").WithSafe("error", err.Error())
	}
	found := false
	for _, n := range nodes {
		if n.NodeID == nodeID {
			found = true
			break
		}
	}
	if !found {
		return errs.New(errs.CodeNotFound, "Unknown cluster node.").WithSafe("nodeId", nodeID)
	}
	if err := s.Bus.PublishCore(bus.ClusterDrainSubject(nodeID), []byte("{}")); err != nil {
		return errs.New(errs.CodeInternalError, "Could not publish the drain signal.").WithSafe("error", err.Error())
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "cluster.drain", audit.Target{Type: "node", ID: nodeID}, nil)
	}
	return nil
}

// queueSettingsDoc is stored at settings._id = "queues".
type queueSettingsDoc struct {
	ID        string                  `bson:"_id"`
	Queues    map[string]queueSetting `bson:"queues"`
	Version   int64                   `bson:"version"`
	UpdatedAt time.Time               `bson:"updatedAt"`
}

type queueSetting struct {
	Paused bool   `bson:"paused"`
	Keep   string `bson:"keep,omitempty"`
}

func (s *Service) loadQueueSettings(ctx context.Context) error {
	var doc queueSettingsDoc
	err := s.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": QueuesDocID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	for name, q := range doc.Queues {
		if q.Paused {
			_ = s.Bus.PublishQueuePaused(name, true)
		} else {
			s.Bus.SetQueuePaused(name, false)
		}
	}
	return nil
}

func (s *Service) readQueueDoc(ctx context.Context) (queueSettingsDoc, error) {
	var doc queueSettingsDoc
	err := s.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": QueuesDocID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return queueSettingsDoc{ID: QueuesDocID, Queues: map[string]queueSetting{}}, nil
	}
	if err != nil {
		return queueSettingsDoc{}, err
	}
	if doc.Queues == nil {
		doc.Queues = map[string]queueSetting{}
	}
	return doc, nil
}

func (s *Service) saveQueueDoc(ctx context.Context, doc queueSettingsDoc) error {
	doc.ID = QueuesDocID
	doc.UpdatedAt = time.Now().UTC()
	_, err := s.DB.Collection(settings.SettingsCollection).ReplaceOne(ctx, bson.M{"_id": QueuesDocID}, doc,
		options.Replace().SetUpsert(true))
	return err
}
