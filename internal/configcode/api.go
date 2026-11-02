package configcode

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P3")
	httpapi.Register(srv, huma.Operation{
		OperationID: "exportConfig", Method: http.MethodGet, Path: "/v1/config/export",
		Summary: "Export organisation config as code", Tags: []string{"config"},
		Description: "Returns the active organisation's managed configuration as an `OrganizationConfig` document: " +
			"organisation-level settings and switches, webhook endpoints, routes and API key metadata. Secrets are " +
			"exported as `${secret:NAME}` placeholders, never as values; API key tokens are never included. Store the " +
			"document in Git (as YAML via the CLI) and change it through plan / apply. Needs the `configAsCode.enabled` " +
			"switch (`403 feature_disabled` otherwise). Requires the `read` permission (API keys `settings:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeFeatureDisabled),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleDocument())}},
		Extensions: httpapi.WithScope(ext, "settings:read"),
	}, m.exportConfig)

	httpapi.Register(srv, huma.Operation{
		OperationID: "planConfig", Method: http.MethodPost, Path: "/v1/config/plan",
		Summary: "Plan organisation config changes", Tags: []string{"config"},
		Description: "Compares a desired `OrganizationConfig` document with the live configuration and returns the ordered " +
			"create / update / delete actions plus a `planHash`; nothing is changed. Resources are matched by name. " +
			"With `prune: true` live endpoints, routes and API keys missing from the document are deleted too. Secret " +
			"changes show only `(changed)`. At most 500 resources per kind. Needs the `configAsCode.enabled` switch. " +
			"Requires `settings.write` (owner, admin; API keys `settings:write`). Supports `Idempotency-Key`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Duplicate webhook endpoint name."}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{"document": exampleDocument(), "prune": false})},
		Responses:   map[string]*huma.Response{"200": {Content: jsonContent(examplePlan())}},
		Extensions:  httpapi.WithScope(ext, "settings:write"),
	}, m.planConfig)

	httpapi.Register(srv, huma.Operation{
		OperationID: "applyConfig", Method: http.MethodPost, Path: "/v1/config/apply",
		Summary: "Apply a config plan", Tags: []string{"config"},
		Description: "Re-plans the document and, when the result still has the `planHash` you reviewed, queues a " +
			"`config.apply` job that executes the actions in order; it answers `202` with the job and " +
			"`Location: /v1/jobs/{jobId}`. If anything changed live in between, nothing is queued (`409 config_drift`): " +
			"plan again. The job stops at the first failing action and reports the applied ones and where it stopped; " +
			"each action writes the same audit records and events as the equivalent API call. `${secret:NAME}` " +
			"references are resolved from this organisation's own `settings.env` secrets (never the server environment or " +
			"files); an undefined name fails with `422 validation_failed` naming it. Needs the `configAsCode.enabled` " +
			"switch. Requires `settings.write` (API keys `settings:write`). Writes a `config.apply.queued` audit record. " +
			"Supports `Idempotency-Key`.",
		DefaultStatus: http.StatusAccepted,
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "planHash is required."},
			httpapi.ProblemDoc{Code: errs.CodeConfigDrift, Detail: "Live configuration changed since this plan was computed."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{
			"document": exampleDocument(), "prune": false,
			"planHash": "sha256:5f2c8e1a9b7d4c3e6f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6",
		})},
		Responses: map[string]*huma.Response{"202": {Content: jsonContent(map[string]any{"job": map[string]any{
			"id": "job_01m5a8dy1erfzgbvqgcb9cqtc4", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
			"kind": "config.apply", "state": "queued", "createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z",
			"createdAt": "2026-10-27T11:00:00Z",
		}})}},
		Extensions: httpapi.WithScope(ext, "settings:write"),
	}, m.applyConfig)
}

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleDocument() map[string]any {
	return map[string]any{
		"apiVersion": APIVersion, "kind": KindOrg,
		"metadata": map[string]any{"organization": "acme-legal"},
		"spec": map[string]any{
			"settings": map[string]any{"webhooks.autoDisableAfter": "5d", "attachments.signedUrlTtl": "15m"},
			"switches": map[string]any{"sandbox.enabled": false, "tracking.opens": true},
			"webhookEndpoints": []any{map[string]any{ //nolint:gosec // G101: documentation example secret ref, not a real credential.
				"name": "crm-inbound", "enabled": true, "url": "https://crm.example.com/hooks/emailbridge",
				"events": []any{"message.received", "message.bounced"}, "secret": "${secret:CRM_WEBHOOK_SECRET}",
			}},
			"routes": []any{map[string]any{
				"name": "invoices-to-crm", "endpoint": "crm-inbound", "enabled": true, "order": 10,
				"filter": `event.type == "message.received" && event.data.subject.contains("Invoice")`,
			}},
			"apiKeys": []any{map[string]any{
				"name": "crm-sync", "scopes": []any{"accounts:read", "messages:read"},
			}},
		},
	}
}

func examplePlan() map[string]any {
	return map[string]any{
		"planHash": "sha256:5f2c8e1a9b7d4c3e6f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6",
		"prune":    false,
		"actions": []any{
			map[string]any{"op": "update", "kind": "switch", "name": "tracking.opens",
				"changes": map[string]any{"value": map[string]any{"before": false, "after": true}}},
			map[string]any{"op": "update", "kind": "webhookEndpoint", "name": "crm-inbound",
				"id": "whe_01m5h2pme18h2xzbq4pdypnrv8", "version": 4,
				"changes": map[string]any{
					"url":    map[string]any{"before": "https://crm.example.com/hooks/old", "after": "https://crm.example.com/hooks/emailbridge"},
					"secret": "(changed)",
				}},
			map[string]any{"op": "create", "kind": "route", "name": "invoices-to-crm",
				"changes": map[string]any{"endpoint": "crm-inbound", "order": 10}},
		},
	}
}

type exportOut struct {
	Body *Document
}

type planIn struct {
	Body PlanRequest
}

type planOut struct {
	Body *Plan
}

type applyIn struct {
	Body ApplyRequest
}

type jobAcceptedBody struct {
	Job any `json:"job" doc:"The queued config.apply job; poll GET /v1/jobs/{jobId}. Its result lists the applied actions and, on failure, where apply stopped." example:"{\"id\":\"job_01m5a8dy1erfzgbvqgcb9cqtc4\",\"organizationId\":\"org_01m5caqpk0kast63yc13wpy2d9\",\"kind\":\"config.apply\",\"state\":\"queued\",\"createdBy\":\"usr_01m5ccvej0dqefkn0xhkcq2q6z\",\"createdAt\":\"2026-10-27T11:00:00Z\"}"`
}

type applyOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the apply job." example:"/v1/jobs/job_01m5a8dy1erfzgbvqgcb9cqtc4"`
	Body     jobAcceptedBody
}

func (m *Module) exportConfig(ctx context.Context, _ *struct{}) (*exportOut, error) {
	doc, err := m.svc.Export(ctx)
	if err != nil {
		return nil, err
	}
	return &exportOut{Body: doc}, nil
}

func (m *Module) planConfig(ctx context.Context, in *planIn) (*planOut, error) {
	plan, err := m.svc.Plan(ctx, in.Body.Document, in.Body.Prune)
	if err != nil {
		return nil, err
	}
	return &planOut{Body: plan}, nil
}

func (m *Module) applyConfig(ctx context.Context, in *applyIn) (*applyOut, error) {
	job, err := m.svc.Apply(ctx, in.Body.Document, in.Body.Prune, in.Body.PlanHash)
	if err != nil {
		return nil, err
	}
	return &applyOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID,
		Body: jobAcceptedBody{Job: job},
	}, nil
}
