package cluster

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/queues"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P2")
	owner := " Instance owners only (`cluster.read` / `cluster.operate`)."
	httpapi.Register(srv, huma.Operation{
		OperationID: "getCluster", Method: http.MethodGet, Path: "/v1/cluster",
		Summary: "Cluster nodes and scheduler leader", Tags: []string{"cluster"},
		Description: "Lists the live EmailBridge nodes from the NATS node registry (roles, version, start and last " +
			"heartbeat, draining flag, accounts and lease partitions held), the totals and the current scheduler " +
			"leader." + owner + " Read-only.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden),
		Extensions: httpapi.WithScope(ext, "cluster:read"),
	}, m.getCluster)

	httpapi.Register(srv, huma.Operation{
		OperationID: "drainClusterNode", Method: http.MethodPost, Path: "/v1/cluster/nodes/{nodeId}/drain",
		Summary: "Drain a cluster node", Tags: []string{"cluster"},
		Description: "Asks a live node to drain before maintenance: it is marked `draining`, receives no new account or " +
			"partition leases, and placement hands its current ones to other nodes. The node keeps running until " +
			"it is stopped; draining ends with a restart." + owner + " Answers `202` (the hand-off is " +
			"asynchronous; watch `GET /v1/cluster`). Repeating it is harmless. `404` for an unknown or dead node.",
		DefaultStatus: http.StatusAccepted,
		Metadata:      httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound),
		Extensions:    httpapi.WithScope(ext, "cluster:operate"),
	}, m.drainNode)

	httpapi.Register(srv, huma.Operation{
		OperationID: "getStats", Method: http.MethodGet, Path: "/v1/stats",
		Summary: "Organisation operational stats", Tags: []string{"stats"},
		Description: "Counters for the active organisation's dashboard: accounts and outbound messages by state, webhook " +
			"deliveries by state and events in the last 24 hours, dead-lettered deliveries, plus the `kpis` " +
			"block (counts, trends and sparkline buckets) for `period` (1h, 24h, 7d or 30d; default 24h) or for a " +
			"custom window `from` / `to` (RFC 3339; 5 minutes to 92 days; a `to` in the future counts up to now; " +
			"the previous window of equal length gives the trend). Any member (`read`; API keys need an " +
			"`accounts:read` scope). Read-only; computed on each call.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(ext, "stats:read"),
	}, m.getStats)

	httpapi.Register(srv, huma.Operation{
		OperationID: "listQueues", Method: http.MethodGet, Path: "/v1/queues",
		Summary: "JetStream work queues", Tags: []string{"queues"},
		Description: "Lists the NATS JetStream work streams (`EMAILBRIDGE_EVENTS`, `EMAILBRIDGE_DELIVERY`, " +
			"`EMAILBRIDGE_SUBMIT`, `EMAILBRIDGE_SYSMAIL`) with their pause state, what they carry (`kind`), whether " +
			"their records can be cancelled on the queue page, and `items` — the counts of the waiting, scheduled, " +
			"retry-waiting, in-flight and dead-lettered records of the active organisation, or of every " +
			"organisation with `scope=all`. Any member that can read outbound messages or deliveries (API keys " +
			"`outbound:read` or `deliveries:read`); the raw stream statistics (`pending`, `ackPending`, `consumers`, " +
			"`keep`), the system-mail counts and `scope=all` are for instance owners only (`cluster.operate`). " +
			"Read-only.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "Only the instance owner can list every organisation."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
		),
		Responses:  map[string]*huma.Response{"200": {Content: queuesExamples()}},
		Extensions: httpapi.WithScope(ext, "outbound:read"),
	}, m.listQueues)

	httpapi.Register(srv, huma.Operation{
		OperationID: "patchQueue", Method: http.MethodPatch, Path: "/v1/queues/{queue}",
		Summary: "Pause, resume or set retention for a queue", Tags: []string{"queues"},
		Description: "Pauses or resumes the consumers of one work stream on every node (`paused`), and/or changes how " +
			"long its messages are kept (`keep`, a duration such as `7d`). Pausing stops processing but keeps " +
			"accepting work, so nothing is lost; resume to drain the backlog. Persisted, so it survives restarts." +
			owner + " At least one field is required.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(ext, "cluster:operate"),
	}, m.patchQueue)
}

type clusterOut struct {
	Body *ClusterView
}

type drainIn struct {
	NodeID string `path:"nodeId" minLength:"1" maxLength:"128" doc:"Node ID as listed by GET /v1/cluster." example:"eb-node-2"`
}

type drainOut struct {
	Status int
}

type statsIn struct {
	Period string `query:"period" enum:"1h,24h,7d,30d,custom" doc:"KPI period: counts, trends and sparkline buckets. Default 24h. Omit (or send custom) when sending from / to." example:"7d"`
	From   string `query:"from" doc:"Start of a custom KPI window (RFC 3339). Needs to; at most 92 days before it." example:"2026-10-01T00:00:00Z"`
	To     string `query:"to" doc:"End of a custom KPI window (RFC 3339); a time in the future counts up to now." example:"2026-10-15T00:00:00Z"`
}

// parseStatsTime parses an optional RFC 3339 query value.
func parseStatsTime(name, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, name+" must be an RFC 3339 time such as 2026-10-01T00:00:00Z.").
			WithSafe("field", name)
	}
	return &t, nil
}

type statsOut struct {
	Body *StatsView
}

type queuesOut struct {
	Body struct {
		Data []QueueView `json:"data" doc:"Every work stream."`
	}
}

type patchQueueIn struct {
	Queue string `path:"queue" minLength:"1" doc:"Stream name from GET /v1/queues." example:"EMAILBRIDGE_DELIVERY"`
	Body  struct {
		Paused *bool   `json:"paused,omitempty" doc:"true pauses the consumers on every node, false resumes them." example:"true"`
		Keep   *string `json:"keep,omitempty" doc:"Message retention (S9 duration such as 72h or 7d)." example:"7d"`
	}
}

type patchQueueOut struct {
	Body *QueueView
}

func (m *Module) getCluster(ctx context.Context, _ *struct{}) (*clusterOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermClusterRead); err != nil {
		return nil, err
	}
	v, err := m.svc.Cluster(ctx)
	if err != nil {
		return nil, err
	}
	return &clusterOut{Body: v}, nil
}

func (m *Module) drainNode(ctx context.Context, in *drainIn) (*drainOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermClusterOperate); err != nil {
		return nil, err
	}
	if err := m.svc.Drain(ctx, in.NodeID); err != nil {
		return nil, err
	}
	return &drainOut{Status: http.StatusAccepted}, nil
}

func (m *Module) getStats(ctx context.Context, in *statsIn) (*statsOut, error) {
	if err := tenancy.RequireRead(ctx, "accounts"); err != nil {
		return nil, err
	}
	v, err := m.svc.Stats(ctx)
	if err != nil {
		return nil, err
	}
	req := KPIRequest{}
	if in != nil {
		req.Period = in.Period
		if req.From, err = parseStatsTime("from", in.From); err != nil {
			return nil, err
		}
		if req.To, err = parseStatsTime("to", in.To); err != nil {
			return nil, err
		}
	}
	k, err := m.svc.KPIs(ctx, req)
	if err != nil {
		return nil, err
	}
	v.KPIs = k
	return &statsOut{Body: v}, nil
}

type listQueuesIn struct {
	Scope string `query:"scope" enum:"organization,all" default:"organization" doc:"Scope of the items counts: the active organisation, or every organisation (instance owner)." example:"organization"`
}

func (m *Module) listQueues(ctx context.Context, in *listQueuesIn) (*queuesOut, error) {
	owner := tenancy.Require(ctx, tenancy.PermClusterOperate) == nil
	all := in != nil && in.Scope == queues.ScopeAll
	if all && !owner {
		return nil, errs.New(errs.CodeForbidden, "Only the instance owner can list every organisation.")
	}
	if !owner {
		if err := tenancy.RequireRead(ctx, "outbound"); err != nil {
			if derr := tenancy.RequireRead(ctx, "deliveries"); derr != nil {
				return nil, err
			}
		}
	}
	data, err := m.svc.Queues(ctx)
	if err != nil {
		return nil, err
	}
	for i := range data {
		if !owner {
			data[i].redact()
		}
		if m.items == nil {
			continue
		}
		c, err := m.items.Counts(ctx, data[i].Name, all)
		if err != nil {
			return nil, err
		}
		data[i].Items = c
	}
	out := &queuesOut{}
	out.Body.Data = data
	return out, nil
}

func (m *Module) patchQueue(ctx context.Context, in *patchQueueIn) (*patchQueueOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermClusterOperate); err != nil {
		return nil, err
	}
	if in.Body.Paused == nil && in.Body.Keep == nil {
		return nil, errs.New(errs.CodeValidationFailed, "Provide paused and/or keep.")
	}
	v, err := m.svc.PatchQueue(ctx, in.Queue, in.Body.Paused, in.Body.Keep)
	if err != nil {
		return nil, err
	}
	return &patchQueueOut{Body: v}, nil
}

// queuesExamples documents GET /v1/queues for an organisation member and for the instance owner.
func queuesExamples() map[string]*huma.MediaType {
	member := []any{
		map[string]any{"name": "EMAILBRIDGE_EVENTS", "kind": "events", "cancellable": false, "paused": false},
		map[string]any{"name": "EMAILBRIDGE_DELIVERY", "kind": "webhooks", "cancellable": true, "paused": false,
			"items": map[string]any{"waiting": 12, "scheduled": 0, "retrying": 3, "inFlight": 1, "deadLettered": 2,
				"oldestAt": "2026-10-26T18:30:41Z"}},
		map[string]any{"name": "EMAILBRIDGE_SUBMIT", "kind": "sending", "cancellable": true, "paused": false,
			"items": map[string]any{"waiting": 42, "scheduled": 7, "retrying": 3, "inFlight": 1, "deadLettered": 0,
				"oldestAt": "2026-10-27T08:41:05Z"}},
		map[string]any{"name": "EMAILBRIDGE_SYSMAIL", "kind": "systemMail", "cancellable": true, "paused": false},
	}
	owner := []any{
		map[string]any{"name": "EMAILBRIDGE_EVENTS", "kind": "events", "cancellable": false, "pending": 1284,
			"ackPending": 16, "consumers": 3, "paused": false, "keep": "7d"},
		map[string]any{"name": "EMAILBRIDGE_DELIVERY", "kind": "webhooks", "cancellable": true, "pending": 57,
			"ackPending": 4, "consumers": 3, "paused": false,
			"items": map[string]any{"waiting": 40, "scheduled": 0, "retrying": 9, "inFlight": 4, "deadLettered": 5,
				"oldestAt": "2026-10-26T18:30:41Z"}},
		map[string]any{"name": "EMAILBRIDGE_SUBMIT", "kind": "sending", "cancellable": true, "pending": 112,
			"ackPending": 6, "consumers": 3, "paused": true,
			"items": map[string]any{"waiting": 96, "scheduled": 31, "retrying": 10, "inFlight": 6, "deadLettered": 0,
				"oldestAt": "2026-10-27T07:12:30Z"}},
		map[string]any{"name": "EMAILBRIDGE_SYSMAIL", "kind": "systemMail", "cancellable": true, "pending": 2,
			"ackPending": 0, "consumers": 3, "paused": false,
			"items": map[string]any{"waiting": 2, "scheduled": 0, "retrying": 0, "inFlight": 0, "deadLettered": 0,
				"oldestAt": "2026-10-27T10:03:15Z"}},
	}
	return map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
		"member": {Summary: "Organisation member: counts of the active organisation", Value: map[string]any{"data": member}},
		"owner":  {Summary: "Instance owner, scope=all: stream statistics and counts of every organisation", Value: map[string]any{"data": owner}},
	}}}
}
