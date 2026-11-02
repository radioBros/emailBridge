package rules

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P1")
	extP3 := httpapi.Phase("P3")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listRoutes", Method: http.MethodGet, Path: "/v1/routes",
		Summary: "List routes", Tags: []string{"routes"},
		Description: "Lists the routes of the active organisation in no particular order (sort with `sort`): each route sends the events " +
			"its CEL `filter` matches to one webhook endpoint, optionally reshaped by `map`. Includes the newest rule " +
			"error (`lastError`). Search with `q` (substring of the name). Pagination: `limit` (default 50, max 200) " +
			"with `cursor`, or `page` with `includeTotal`; sort `name`, `order`, `createdAt` or `id` (prefix `-`, " +
			"default `-id`). API keys with per-route scopes see only those routes. Requires the `read` permission " +
			"(API keys `routes:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRouteList())}},
		Extensions: httpapi.WithScope(ext, "routes:read"),
	}, m.listRoutes)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createRoute", Method: http.MethodPost, Path: "/v1/routes",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create route", Tags: []string{"routes"},
		Description: "Creates a route: every stored event whose CEL `filter` is true is delivered to `endpointId` " +
			"(body = the envelope, or the result of `map`). Give either a CEL `filter` or a no-code `builder`, which " +
			"is compiled to the filter; an empty filter matches everything. The expressions are compiled and " +
			"cost-checked before saving (`422 route_compile_error`) and saved as version 1. Routes run in `order`. " +
			"An endpoint that has its own rule (rule mode) does not receive route deliveries. Requires " +
			"`webhooks.write` (owner, admin, operator; API keys `webhooks:write`). Supports `Idempotency-Key`; " +
			"returns `Location` and `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "name is required."},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError, Detail: "filter: undeclared reference to 'subjctContains'."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeRouteNameTaken},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(createRouteExamples())},
		Responses:   map[string]*huma.Response{"201": {Content: jsonContent(exampleRoute())}},
		Extensions:  httpapi.WithScope(ext, "routes:write"),
	}, m.createRoute)
	// Builder endpoints before /{routeId} so huma does not treat "builder" as an id.
	httpapi.Register(srv, huma.Operation{
		OperationID: "compileRouteBuilder", Method: http.MethodPost, Path: "/v1/routes/builder/compile",
		Summary: "Compile a rule builder model to CEL", Tags: []string{"routes"},
		Description: "Compiles a no-code builder model to the CEL filter it stands for and checks that it compiles, " +
			"without saving anything; the admin rule editor uses it to show the CEL next to the builder. Invalid " +
			"models (unknown field, wrong operator or value type, nesting deeper than 8) answer `422`. Requires " +
			"`webhooks.write` (API keys `webhooks:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "unknown builder field \"sender\""},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{"builder": exampleBuilder()})},
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{
			"cel": exampleCompiled, "ok": true})}},
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.compileBuilder)
	httpapi.Register(srv, huma.Operation{
		OperationID: "expressibleRouteBuilder", Method: http.MethodPost, Path: "/v1/routes/builder/expressible",
		Summary: "Best-effort reverse of builder-emitted CEL", Tags: []string{"routes"},
		Description: "Best-effort reverse of `compileRouteBuilder`: tells whether a CEL filter can be shown in the " +
			"no-code builder and returns the builder model when it can (simple comparisons joined by && or ||). " +
			"Anything else answers `expressible: false` and stays in the code editor. Nothing is saved. Requires " +
			"`webhooks.write` (API keys `webhooks:write`).",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{
			"cel": `(type == "message.received") && (data.subject.contains("invoice"))`})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"expressible": {Summary: "Shown in the builder", Value: map[string]any{"expressible": true,
				"builder": map[string]any{"match": "all", "conditions": []any{
					map[string]any{"field": "type", "op": "eq", "value": "message.received"},
					map[string]any{"field": "subject", "op": "contains", "value": "invoice"}}}}},
			"code": {Summary: "Stays in the code editor", Value: map[string]any{"expressible": false}},
		})}},
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.expressibleBuilder)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getRoute", Method: http.MethodGet, Path: "/v1/routes/{routeId}",
		Summary: "Get route", Tags: []string{"routes"},
		Description: "Returns one route with its current filter, map and builder, its newest rule error and the " +
			"`ETag` to send as `If-Match`. Requires the `read` permission (API keys `routes:read` or " +
			"`routes:read:<routeId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRoute())}},
		Extensions: httpapi.WithScope(ext, "routes:read"),
	}, m.getRoute)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchRoute", Method: http.MethodPatch, Path: "/v1/routes/{routeId}",
		Summary: "Update route", Tags: []string{"routes"},
		Description: "Changes the given fields. A new `filter`, `map` or `builder` is compiled first " +
			"(`422 route_compile_error`) and saved as a new version; the router picks it up within seconds. Send " +
			"the `ETag` as `If-Match` (`412 version_mismatch`). Requires `webhooks.write` (API keys " +
			"`webhooks:write` or `webhooks:write:<routeId>`). Returns the route and its new `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "name must not be empty."},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError, Detail: "map: syntax error at 1:14."},
			httpapi.ProblemDoc{Code: errs.CodeRouteNameTaken},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(patchRouteExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonContent(exampleRoute())}},
		Extensions:  httpapi.WithScope(ext, "routes:write"),
	}, m.patchRoute)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteRoute", Method: http.MethodDelete, Path: "/v1/routes/{routeId}",
		Summary: "Delete route", Tags: []string{"routes"},
		Description: "Deletes the route; events are no longer routed by it (its saved versions stay). Existing deliveries stay " +
			"in the history. Optional `If-Match`. Requires `webhooks.write` (API keys `webhooks:write` or " +
			"`webhooks:write:<routeId>`). Answers `204` without a body.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(ext, "routes:write"),
	}, m.deleteRoute)
	httpapi.Register(srv, huma.Operation{
		OperationID: "testRoute", Method: http.MethodPost, Path: "/v1/routes/{routeId}/test",
		Summary: "Evaluate route against an event", Tags: []string{"routes"},
		Description: "Evaluates the route (or a draft `filter` / `map` / `builder` sent in the body) against one " +
			"event: an inline CloudEvents envelope (`event`), a stored event (`eventId`) or a built-in `sample` of a " +
			"type. Returns whether it matched, the mapped body, the CEL cost, a per-term trace of the filter and, for " +
			"a draft that does not compile, the error position. Nothing is delivered or saved. Requires " +
			"`webhooks.write` (API keys `webhooks:write` or `webhooks:write:<routeId>`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "event, eventId or sample is required"}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(testRouteExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonExamples(testRouteResultExamples())}},
		Extensions:  httpapi.WithScope(ext, "routes:write"),
	}, m.testRoute)
	httpapi.Register(srv, huma.Operation{
		OperationID: "listRouteVersions", Method: http.MethodGet, Path: "/v1/routes/{routeId}/versions",
		Summary: "List route expression versions", Tags: []string{"routes"},
		Description: "Lists the saved versions of the route's filter, map and builder, newest first (every change " +
			"saves one). Paged with `limit` (default 50, max 200) and `page`; `includeTotal` adds the count. Requires " +
			"the `read` permission (API keys `routes:read`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRouteVersions())}},
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.listVersions)
	httpapi.Register(srv, huma.Operation{
		OperationID: "restoreRouteVersion", Method: http.MethodPost, Path: "/v1/routes/{routeId}/versions/{version}/restore",
		Summary: "Restore a prior route version", Tags: []string{"routes"},
		Description: "Makes a saved version the route's current filter / map / builder again: it is compiled again " +
			"and saved as a new version, so the history is kept. Optional `If-Match` with the route's `ETag`. " +
			"Requires `webhooks.write` (API keys `webhooks:write`). Writes a `route.restore` audit record. Returns " +
			"the route and its new `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route version not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "version must be >= 1"},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRoute())}},
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.restoreVersion)
	httpapi.Register(srv, huma.Operation{
		OperationID: "dryRunRoute", Method: http.MethodPost, Path: "/v1/routes/{routeId}/dry-run",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Dry-run a route against recent stored events", Tags: []string{"routes"},
		Description: "Starts a `rules.dryRun` job that runs the route (or the draft in the body) over the stored " +
			"events of the last `windowHours` (at most 24 h, at most 1000 events) and reports how many matched, up " +
			"to 20 mapped samples, errors per phase and the CEL cost. Nothing is delivered. Answers `202` with the " +
			"job; follow it at `Location` (`GET /v1/jobs/{jobId}`). Requires `webhooks.write` (API keys " +
			"`webhooks:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonContent(exampleRouteDryRunRequest())},
		Responses:   map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted())}},
		Extensions:  httpapi.WithScope(extP3, "routes:write"),
	}, m.dryRunRoute)
	// W73
	httpapi.Register(srv, huma.Operation{
		OperationID: "listRouteTestEvents", Method: http.MethodGet, Path: "/v1/routes/test-events",
		Summary: "Recent org events for the rule test picker", Tags: []string{"routes"},
		Description: "Lists recent stored events of the organisation (newest first, ID, type, time, account and " +
			"subject only) for the event picker of the rule test panel; filter by `type` and `accountId`. An " +
			"account-bound API key sees only its account. Requires the `read` permission (API keys `routes:read`). " +
			"Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}),
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.listTestEvents)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getRouteStats", Method: http.MethodGet, Path: "/v1/routes/{routeId}/stats",
		Summary: "Counters for a route", Tags: []string{"routes"},
		Description: "Returns the route's counters across all nodes: events evaluated, matched, skipped and failed, " +
			"with the time of the last match and the last error. Counters are flushed every second, so they can lag " +
			"slightly. Requires the `read` permission (API keys `routes:read` or `routes:read:<routeId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."}),
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.getRouteStats)
	httpapi.Register(srv, huma.Operation{
		OperationID: "listRouteDecisions", Method: http.MethodGet, Path: "/v1/routes/{routeId}/decisions",
		Summary: "Recent filter decisions for a route", Tags: []string{"routes"},
		Description: "Lists the route's most recent filter decisions (at most 50 are kept), newest first: the event, " +
			"whether it matched, the first false term when it did not, or the evaluation error. Requires the `read` " +
			"permission (API keys `routes:read` or `routes:read:<routeId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Route not found."}),
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.listRouteDecisions)
	httpapi.Register(srv, huma.Operation{
		OperationID: "listRuleLists", Method: http.MethodGet, Path: "/v1/rule-lists",
		Summary: "List organisation named lists", Tags: []string{"rule-lists"},
		Description: "Lists the organisation's named lists (addresses, domains or keywords) that CEL rules check with " +
			"`inList(\"name\", value)`, without their entries (`entryCount` only; GET one list for the entries). " +
			"Search with `q` (substring of the name), filter by `kind`; paged with `limit` (default 50, max 100) and " +
			"`page`, sort `name`, `updatedAt` or `entryCount` (prefix `-`, default `name`). Requires the `read` " +
			"permission (API keys `routes:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRuleListPage())}},
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.listRuleLists)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createRuleList", Method: http.MethodPost, Path: "/v1/rule-lists",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create a named list", Tags: []string{"rule-lists"},
		Description: "Creates a named list for `inList()`: a `name` of lower-case letters, digits, `-` and `_` (at " +
			"most 64), a `kind` (addresses, domains or keywords) and up to 5000 entries (lower-cased and " +
			"de-duplicated, at most 256 bytes each). Rules see changes within 5 seconds (list cache). Requires " +
			"`webhooks.write` (API keys `webhooks:write`). Writes a `rule_list.create` audit record. Supports " +
			"`Idempotency-Key`; returns `Location` and `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "kind must be addresses, domains or keywords"},
			httpapi.ProblemDoc{Code: errs.CodeRuleListNameTaken},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{"name": "vip-customers", "kind": "addresses",
			"entries": []any{"giulia.bianchi@example.org", "m.rossi@example.net", "studio.verdi@example.com"}})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonContent(exampleRuleList(true))}},
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.createRuleList)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getRuleList", Method: http.MethodGet, Path: "/v1/rule-lists/{listId}",
		Summary: "Get a named list", Tags: []string{"rule-lists"},
		Description: "Returns one named list with its entries and the `ETag` to send as `If-Match`. Requires the " +
			"`read` permission (API keys `routes:read`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Rule list not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRuleList(true))}},
		Extensions: httpapi.WithScope(extP3, "routes:read"),
	}, m.getRuleList)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchRuleList", Method: http.MethodPatch, Path: "/v1/rule-lists/{listId}",
		Summary: "Update a named list", Tags: []string{"rule-lists"},
		Description: "Renames the list and/or replaces all its entries (send the full new set). Rules referencing the " +
			"old name stop matching it. Send the `ETag` as `If-Match` (`412 version_mismatch`). Requires " +
			"`webhooks.write` (API keys `webhooks:write`). Writes a `rule_list.update` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Rule list not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "entries must contain at most 5000 values"},
			httpapi.ProblemDoc{Code: errs.CodeRuleListNameTaken},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{
			"entries": []any{"giulia.bianchi@example.org", "m.rossi@example.net", "studio.verdi@example.com", "a.neri@example.org"}})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRuleList(true))}},
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.patchRuleList)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteRuleList", Method: http.MethodDelete, Path: "/v1/rule-lists/{listId}",
		Summary: "Delete a named list", Tags: []string{"rule-lists"},
		Description: "Deletes the named list; `inList()` calls that name it return false from then on. Optional " +
			"`If-Match`. Requires `webhooks.write` (API keys `webhooks:write`). Writes a `rule_list.delete` audit " +
			"record. Answers `204` without a body.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Rule list not found."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(extP3, "routes:write"),
	}, m.deleteRuleList)
	documentBuilderBodies(srv)
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6InJ0ZV8wMW01ZGFmeTQwcWhzbTNiMnF4MGVmNnNraCJ9"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"name, order, createdAt or id; prefix - for descending. Default -id." example:"order"`
	Q            string `query:"q" doc:"Case-insensitive substring of the route name (at most 200 characters)." example:"invoice"`
}

type listOut struct {
	Body ListResult
}

type createIn struct {
	Body RouteCreate
}

type routeOut struct {
	ETag     string `header:"ETag"`
	Location string `header:"Location" doc:"URL of the new route." example:"/v1/routes/rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Status   int
	Body     Route
}

type routeIDIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
}

type getOut struct {
	ETag string `header:"ETag"`
	Body Route
}

type patchIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	IfMatch string `header:"If-Match"`
	Body    RouteUpdate
}

type deleteIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	IfMatch string `header:"If-Match"`
}

type testIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Body    RouteTestRequest
}

type testOut struct {
	Body RouteTestResult
}

func (m *Module) listRoutes(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.repo.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) createRoute(ctx context.Context, in *createIn) (*routeOut, error) {
	r, err := m.repo.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &routeOut{
		Status:   http.StatusCreated,
		ETag:     formatETag(r.Version),
		Location: "/v1/routes/" + r.ID,
		Body:     *r,
	}, nil
}

func (m *Module) getRoute(ctx context.Context, in *routeIDIn) (*getOut, error) {
	r, err := m.repo.Get(ctx, in.RouteID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(r.Version), Body: *r}, nil
}

func (m *Module) patchRoute(ctx context.Context, in *patchIn) (*getOut, error) {
	r, err := m.repo.Update(ctx, in.RouteID, in.Body, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(r.Version), Body: *r}, nil
}

func (m *Module) deleteRoute(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.repo.Delete(ctx, in.RouteID, in.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) testRoute(ctx context.Context, in *testIn) (*testOut, error) {
	res, err := m.repo.Test(ctx, in.RouteID, in.Body)
	if err != nil {
		return nil, err
	}
	return &testOut{Body: *res}, nil
}

type compileBuilderIn struct {
	Body struct {
		Builder Builder `json:"builder"`
	}
}

type compileBuilderOut struct {
	Body BuilderCompileResult
}

type expressibleIn struct {
	Body struct {
		CEL string `json:"cel"`
	}
}

type expressibleOut struct {
	Body BuilderExpressibleResult
}

type versionsIn struct {
	RouteID      string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Page         int    `query:"page" doc:"1-based page number." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total." example:"true"`
}

type versionsOut struct {
	Body VersionListResult
}

type restoreIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Version int    `path:"version" doc:"Version number to restore." example:"2"`
	IfMatch string `header:"If-Match"`
}

type dryRunIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Body    DryRunRequest
}

type jobAcceptedBody struct {
	Job any `json:"job" doc:"The queued job (GET /v1/jobs/{jobId} to follow it)"`
}

type dryRunOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     jobAcceptedBody
}

func (m *Module) compileBuilder(ctx context.Context, in *compileBuilderIn) (*compileBuilderOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	res, err := m.engine.CompileBuilder(in.Body.Builder)
	if err != nil {
		return nil, err
	}
	return &compileBuilderOut{Body: *res}, nil
}

func (m *Module) expressibleBuilder(ctx context.Context, in *expressibleIn) (*expressibleOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	return &expressibleOut{Body: Expressible(in.Body.CEL)}, nil
}

func (m *Module) listVersions(ctx context.Context, in *versionsIn) (*versionsOut, error) {
	res, err := m.repo.ListVersions(ctx, in.RouteID, VersionListFilter{
		Limit: in.Limit, Page: in.Page, IncludeTotal: in.IncludeTotal,
	})
	if err != nil {
		return nil, err
	}
	return &versionsOut{Body: *res}, nil
}

func (m *Module) restoreVersion(ctx context.Context, in *restoreIn) (*getOut, error) {
	r, err := m.repo.RestoreVersion(ctx, in.RouteID, in.Version, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(r.Version), Body: *r}, nil
}

func (m *Module) dryRunRoute(ctx context.Context, in *dryRunIn) (*dryRunOut, error) {
	job, err := m.repo.StartDryRun(ctx, in.RouteID, in.Body)
	if err != nil {
		return nil, err
	}
	return &dryRunOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID,
		Body: jobAcceptedBody{Job: job},
	}, nil
}

type testEventsIn struct {
	Type      string `query:"type" doc:"Only events of this type." example:"message.received"`
	AccountID string `query:"accountId" doc:"Only events of this account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Limit     int    `query:"limit" doc:"Number of events (default 50, at most 100)." example:"20"`
}

type testEventsOut struct {
	Body TestEventList
}

func (m *Module) listTestEvents(ctx context.Context, in *testEventsIn) (*testEventsOut, error) {
	res, err := m.repo.ListTestEvents(ctx, TestEventsFilter{Type: in.Type, AccountID: in.AccountID, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &testEventsOut{Body: *res}, nil
}

type statsOut struct {
	Body RuleStats
}

func (m *Module) getRouteStats(ctx context.Context, in *routeIDIn) (*statsOut, error) {
	st, err := m.repo.GetStats(ctx, "route", in.RouteID)
	if err != nil {
		return nil, err
	}
	return &statsOut{Body: *st}, nil
}

type decisionsIn struct {
	RouteID string `path:"routeId" doc:"Route ID (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	Limit   int    `query:"limit" doc:"Number of decisions (default and at most 50)." example:"20"`
}

type decisionsOut struct {
	Body RuleDecisionList
}

func (m *Module) listRouteDecisions(ctx context.Context, in *decisionsIn) (*decisionsOut, error) {
	res, err := m.repo.ListDecisions(ctx, "route", in.RouteID, in.Limit)
	if err != nil {
		return nil, err
	}
	return &decisionsOut{Body: *res}, nil
}

type listListsIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 100)." example:"50"`
	Page         int    `query:"page" doc:"1-based page number." example:"1"`
	Q            string `query:"q" doc:"Case-insensitive substring of the list name (W73b)." example:"vip"`
	Kind         string `query:"kind" doc:"addresses, domains or keywords." example:"addresses"`
	Sort         string `query:"sort" doc:"name, updatedAt or entryCount; - prefix = descending." example:"-updatedAt"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total." example:"true"`
}

type listListsOut struct {
	Body RuleListPage
}

type createListIn struct {
	Body RuleListCreate
}

type listIDIn struct {
	ListID string `path:"listId" doc:"Named list ID (rls_…)." example:"rls_01m5dn2nz0q7h3ncd1j49hxtnb"`
}

type listOutOne struct {
	ETag     string `header:"ETag"`
	Location string `header:"Location" doc:"URL of the new list." example:"/v1/rule-lists/rls_01m5dn2nz0q7h3ncd1j49hxtnb"`
	Status   int
	Body     RuleList
}

type getListOut struct {
	ETag string `header:"ETag"`
	Body RuleList
}

type patchListIn struct {
	ListID  string `path:"listId" doc:"Named list ID (rls_…)." example:"rls_01m5dn2nz0q7h3ncd1j49hxtnb"`
	IfMatch string `header:"If-Match"`
	Body    RuleListUpdate
}

type deleteListIn struct {
	ListID  string `path:"listId" doc:"Named list ID (rls_…)." example:"rls_01m5dn2nz0q7h3ncd1j49hxtnb"`
	IfMatch string `header:"If-Match"`
}

func (m *Module) listRuleLists(ctx context.Context, in *listListsIn) (*listListsOut, error) {
	res, err := m.repo.ListListsPage(ctx, ListListsFilter{
		Limit: in.Limit, Page: in.Page, Q: in.Q, Kind: in.Kind, Sort: in.Sort, IncludeTotal: in.IncludeTotal,
	})
	if err != nil {
		return nil, err
	}
	return &listListsOut{Body: *res}, nil
}

func (m *Module) createRuleList(ctx context.Context, in *createListIn) (*listOutOne, error) {
	l, err := m.repo.CreateList(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &listOutOne{
		Status: http.StatusCreated, ETag: formatETag(l.Version),
		Location: "/v1/rule-lists/" + l.ID, Body: *l,
	}, nil
}

func (m *Module) getRuleList(ctx context.Context, in *listIDIn) (*getListOut, error) {
	l, err := m.repo.GetList(ctx, in.ListID)
	if err != nil {
		return nil, err
	}
	return &getListOut{ETag: formatETag(l.Version), Body: *l}, nil
}

func (m *Module) patchRuleList(ctx context.Context, in *patchListIn) (*getListOut, error) {
	l, err := m.repo.UpdateList(ctx, in.ListID, in.Body, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &getListOut{ETag: formatETag(l.Version), Body: *l}, nil
}

func (m *Module) deleteRuleList(ctx context.Context, in *deleteListIn) (*struct{}, error) {
	if err := m.repo.DeleteList(ctx, in.ListID, in.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}
