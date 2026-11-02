package jobs

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Module provides *Service on every node (any module may create jobs or register handlers), runs a runner
// on every node — each claims only the kinds it has handlers for (atomic claim, so a job runs once) — and
// serves GET /v1/jobs/{jobId} on api nodes.
type Module struct {
	Retention   func() time.Duration
	Concurrency int
	Log         *slog.Logger
	svc         *Service
	cancel      context.CancelFunc
	done        chan struct{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "jobs" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.svc = &Service{DB: db, Retention: m.Retention, Log: log, NodeID: a.NodeID}
	if rep, ok := app.Get[*observability.Reporter](a); ok {
		m.svc.CrashReporter = rep
	}
	app.Provide(a, m.svc)
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel, m.done = cancel, make(chan struct{})
	go func() { defer close(m.done); m.svc.Run(rctx, m.Concurrency) }()
	if srv, ok := app.Get[*httpapi.Server](a); ok && a.Roles.Has(app.RoleAPI) {
		httpapi.Register(srv, huma.Operation{
			OperationID: "getJob", Method: http.MethodGet, Path: "/v1/jobs/{jobId}",
			Summary: "Get job status", Tags: []string{"jobs"},
			Description: "Returns the state of a long-running job (organisation or account erasure, exports …) " +
				"started by another operation, which answered `202` with `Location: /v1/jobs/{jobId}`. Poll until " +
				"`state` is `succeeded` (see `result`), `failed` (see `error`) or `cancelled`; `progress` shows how " +
				"far a running job got. Any member of the job's organisation (`read`; API keys need `jobs:read`); " +
				"jobs of other organisations are `404`. Read-only.",
			Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound),
			Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
				"running": {Summary: "Running job", Value: map[string]any{
					"id": "job_01m5cnaee0w96fc46z350nc6q0", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "kind": "organization.erasure",
					"state": "running", "progress": map[string]any{"done": 1800, "total": 4200}, "createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z",
					"createdAt": "2026-10-24T15:00:00Z", "startedAt": "2026-10-24T15:00:02Z",
				}},
				"failed": {Summary: "Failed job", Value: map[string]any{
					"id": "job_01m5cqe6d0rs9cyxvy69ynf0t6", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "kind": "organization.erasure",
					"state": "failed", "createdAt": "2026-10-24T15:00:00Z", "startedAt": "2026-10-24T15:00:02Z", "finishedAt": "2026-10-24T15:03:10Z",
					"error": map[string]any{"type": "https://emailbridge.dev/problems/service_unavailable", "title": "Service unavailable",
						"status": 503, "detail": "Object storage did not answer; the job will not retry automatically.", "code": "service_unavailable", "kind": "temporary"},
				}},
			}}}}},
			Extensions: httpapi.WithScope(httpapi.Phase("P0"), "jobs:read"),
		}, m.getJob)
	}
	return nil
}

// Stop implements app.Module: running jobs are cancelled and re-queued by the next runner (stale heartbeat).
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
	return nil
}

type getJobIn struct {
	JobID string `path:"jobId" pattern:"^job_[0-9a-z]{26}$" doc:"Job ID from the Location header of the operation that started it." example:"job_01m5cnaee0w96fc46z350nc6q0"`
}

type getJobOut struct {
	Body *Job
}

func (m *Module) getJob(ctx context.Context, in *getJobIn) (*getJobOut, error) {
	if err := tenancy.RequireRead(ctx, "jobs"); err != nil {
		return nil, err
	}
	j, err := m.svc.Get(ctx, in.JobID)
	if err != nil {
		return nil, err
	}
	return &getJobOut{Body: j}, nil
}
