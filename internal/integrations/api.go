package integrations

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P8")
	httpapi.Register(server, huma.Operation{
		OperationID: "listIntegrations", Method: http.MethodGet, Path: "/v1/integrations",
		Summary: "List integration catalogue", Tags: []string{"integrations"},
		Description: "Lists the connectors this instance offers (chat, helpdesk, automation, sending, spam, storage, " +
			"CRM, SSO, observability) with the JSON Schema of their configuration, which fields are secrets, their " +
			"capabilities and how many instances the organisation has installed. Filter with `q` (substring of " +
			"name, description or ID) and `category`. Requires the `read` permission (API keys " +
			"`integrations:read`). Read-only.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleCatalogue())}},
		Extensions: httpapi.WithScope(phase, "integrations:read"),
	}, m.catalogue)
	httpapi.Register(server, huma.Operation{
		OperationID: "installIntegration", Method: http.MethodPost, Path: "/v1/integrations/{type}/install",
		DefaultStatus: http.StatusCreated,
		Summary:       "Install a connector", Tags: []string{"integrations"},
		Description: "Installs a connector: validates `config` against its `configSchema`, stores `secrets` " +
			"encrypted and creates the webhook endpoints, routes, settings or SMTP gateway the connector needs " +
			"(listed in `createdResources`; the instance owns them and uninstall removes them). `runTest` runs the " +
			"connector test right away. Some connectors (e.g. Sentry) may only be installed in the instance owner's " +
			"organisation (`403 forbidden`). Requires `settings.write` (owner, admin; API keys `settings:write`). " +
			"Writes an `integration.installed` audit record. Supports `Idempotency-Key`; returns `Location` and " +
			"`ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "Sentry may only be installed in the instance owner's organisation."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Unknown integration type."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "config.webhookUrl is required.",
				Errors: []errs.ProblemItem{{Path: "body.config.webhookUrl", Code: "required", Message: "webhookUrl is required"}}},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{
			"name": "Slack: #support-mail", "runTest": true,
			"config": map[string]any{"webhookUrl": "https://hooks.slack.com/services/T0000000/B0000000/XXXXXXXXXXXXXXXXXXXXXXXX",
				"channelHint": "#support-mail"}})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonContent(exampleSlackView())}},
		Extensions: httpapi.WithScope(phase, "integrations:write"),
	}, m.install)
	httpapi.Register(server, huma.Operation{
		OperationID: "listIntegrationInstances", Method: http.MethodGet, Path: "/v1/integration-instances",
		Summary: "List integration instances", Tags: []string{"integrations"},
		Description: "Lists the installed integration instances of the active organisation with secrets redacted " +
			"(`secretsSet` only) and the resources each one owns. Search with `q` (substring of name or type), filter " +
			"by `type`. Pagination: `limit` (default 50, max 200) with `cursor`, or `page` with `includeTotal`; sort " +
			"`id`, `name` or `createdAt` (default `-createdAt`). Requires the `read` permission (API keys " +
			"`integrations:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleInstanceList())}},
		Extensions: httpapi.WithScope(phase, "integrations:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "getIntegrationInstance", Method: http.MethodGet, Path: "/v1/integration-instances/{instanceId}",
		Summary: "Get integration instance", Tags: []string{"integrations"},
		Description: "Returns one integration instance with secrets redacted, the resources it owns, the status of " +
			"its last connector action and the `ETag` to send as `If-Match`. Requires the `read` permission (API keys " +
			"`integrations:read`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The integration instance does not exist."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleSlackView())}},
		Extensions: httpapi.WithScope(phase, "integrations:read"),
	}, m.get)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchIntegrationInstance", Method: http.MethodPatch, Path: "/v1/integration-instances/{instanceId}",
		Summary: "Update integration instance", Tags: []string{"integrations"},
		Description: "Renames the instance, merges `config` keys, replaces the given `secrets`, and enables or " +
			"disables it together with the endpoints and routes it created. The merged config is validated against " +
			"the connector's schema again. Send the `ETag` as `If-Match` (`412 version_mismatch`). Requires " +
			"`settings.write` (API keys `settings:write`). Writes an `integration.updated` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The integration instance does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "name is invalid."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{"name": "Slack: #mail-alerts",
			"config": map[string]any{"channelHint": "#mail-alerts"}, "enabled": true})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleSlackView())}},
		Extensions: httpapi.WithScope(phase, "integrations:write"),
	}, m.patch)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteIntegrationInstance", Method: http.MethodDelete, Path: "/v1/integration-instances/{instanceId}",
		Summary: "Uninstall integration instance", Tags: []string{"integrations"},
		Description: "Uninstalls the instance: deletes the webhook endpoints, routes, settings and gateways it " +
			"created, then the instance and its stored secrets. Optional `If-Match`. Requires `settings.write` (API " +
			"keys `settings:write`). Writes an `integration.uninstalled` audit record. Answers `204` without a body.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The integration instance does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeConflict},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(phase, "integrations:write"),
	}, m.delete)
	httpapi.Register(server, huma.Operation{
		OperationID: "testIntegrationInstance", Method: http.MethodPost, Path: "/v1/integration-instances/{instanceId}/test",
		Summary: "Test integration instance", Tags: []string{"integrations"},
		Description: "Runs the connector's own check with the stored config and secrets (for example a test message " +
			"to Slack, a ping to the Rspamd controller) and returns whether it passed, how long it took and a " +
			"redacted detail. A failed check is `200` with `ok: false`. Outbound requests go through the egress " +
			"policy. Requires `settings.write` (API keys `settings:write`). Writes an `integration.tested` audit " +
			"record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The integration instance does not exist."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{"ok": true, "latencyMs": 312,
			"detail": "Slack accepted the test message (HTTP 200)."})}},
		Extensions: httpapi.WithScope(phase, "integrations:write"),
	}, m.test)
}

type catalogueIn struct {
	Q        string `query:"q" doc:"Case-insensitive substring of the connector name, description or ID." example:"slack"`
	Category string `query:"category" doc:"Only this category." example:"chat"`
}

type catalogueOut struct {
	Body Catalogue
}

type installIn struct {
	Type string `path:"type" doc:"Connector ID from the catalogue." example:"slack"`
	Body InstallInput
}

type installOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new instance." example:"/v1/integration-instances/int_01m5dzndt02er7qagrrs531p8g"`
	ETag     string `header:"ETag"`
	Body     View
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImludF8wMW01ZHpuZHQwMmVyN3FhZ3JyczUzMXA4ZyJ9"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"id, name or createdAt; prefix - for descending. Default -createdAt." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive substring of the name or connector type (at most 200 characters)." example:"slack"`
	Type         string `query:"type" doc:"Only instances of this connector." example:"slack"`
}

type listOut struct {
	Body List
}

type idIn struct {
	InstanceID string `path:"instanceId" doc:"Integration instance ID (int_…)." example:"int_01m5dzndt02er7qagrrs531p8g"`
}

type getOut struct {
	ETag string `header:"ETag"`
	Body View
}

type patchIn struct {
	InstanceID string `path:"instanceId" doc:"Integration instance ID (int_…)." example:"int_01m5dzndt02er7qagrrs531p8g"`
	IfMatch    string `header:"If-Match"`
	Body       PatchInput
}

type deleteIn struct {
	InstanceID string `path:"instanceId" doc:"Integration instance ID (int_…)." example:"int_01m5dzndt02er7qagrrs531p8g"`
	IfMatch    string `header:"If-Match"`
}

type testOut struct {
	Body TestResult
}

func (m *Module) catalogue(ctx context.Context, in *catalogueIn) (*catalogueOut, error) {
	res, err := m.svc.Catalogue(ctx, CatalogueFilter{Q: in.Q, Category: in.Category})
	if err != nil {
		return nil, err
	}
	return &catalogueOut{Body: *res}, nil
}

func (m *Module) install(ctx context.Context, in *installIn) (*installOut, error) {
	v, _, err := m.svc.Install(ctx, in.Type, in.Body)
	if err != nil {
		return nil, err
	}
	return &installOut{
		Status: http.StatusCreated, Location: "/v1/integration-instances/" + v.ID,
		ETag: formatETag(v.Version), Body: *v,
	}, nil
}

func (m *Module) list(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Q: in.Q, Type: in.Type,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) get(ctx context.Context, in *idIn) (*getOut, error) {
	v, err := m.svc.Get(ctx, in.InstanceID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(v.Version), Body: *v}, nil
}

func (m *Module) patch(ctx context.Context, in *patchIn) (*getOut, error) {
	v, err := m.svc.Patch(ctx, in.InstanceID, in.IfMatch, in.Body)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(v.Version), Body: *v}, nil
}

func (m *Module) delete(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.svc.Uninstall(ctx, in.InstanceID, in.IfMatch); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

func (m *Module) test(ctx context.Context, in *idIn) (*testOut, error) {
	res, err := m.svc.Test(ctx, in.InstanceID)
	if err != nil {
		return nil, err
	}
	return &testOut{Body: *res}, nil
}
