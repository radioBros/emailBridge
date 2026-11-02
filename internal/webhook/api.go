package webhook

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/rules"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P1")
	httpapi.Register(server, huma.Operation{
		OperationID: "listWebhookEndpoints", Method: http.MethodGet, Path: "/v1/webhook-endpoints",
		Summary: "List webhook endpoints", Tags: []string{"webhook-endpoints"},
		Description: "Lists the webhook endpoints of the active organisation with secrets redacted (`secretPreview` only; " +
			"secret header values are never returned), each with its circuit-breaker state and newest delivery. " +
			"Search with `q` (substring of name or URL). Pagination: `limit` (default 50, max 200) with `cursor`, or " +
			"`page` with `includeTotal`; sort `id`, `name` or `createdAt` (prefix `-`, default `-id`). API keys with " +
			"per-endpoint scopes see only those endpoints. Requires the `read` permission (API keys `webhooks:read`). " +
			"Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleEndpointList())}},
		Extensions: httpapi.WithScope(phase, "webhook-endpoints:read"),
	}, m.listEndpoints)
	httpapi.Register(server, huma.Operation{
		OperationID: "createWebhookEndpoint", Method: http.MethodPost, Path: "/v1/webhook-endpoints",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create webhook endpoint", Tags: []string{"webhook-endpoints"},
		Description: "Creates a webhook endpoint that receives the subscribed `events` (or `*`) as signed POST " +
			"requests: the CloudEvents envelope as JSON, signed with Standard Webhooks headers (`webhook-id`, " +
			"`webhook-timestamp`, `webhook-signature`) or, in `bodyOnly` mode, `eb-signature`; every request also " +
			"carries `eb-event-type`, `eb-delivery-id` and `eb-attempt`. The URL is checked against the egress policy " +
			"(`422 endpoint_url_blocked` for private or blocked targets). A signing secret is generated when omitted " +
			"(rotate it to read it once). Optional: account / folder `filter`, custom `headers` (secret values stored " +
			"encrypted), `payload` options, retry and ordering policy, mTLS (`webhook.mtls` switch) and a CEL `rule` " +
			"that filters and reshapes deliveries. Sink kinds other than `http` need their feature switch. Requires " +
			"`webhooks.write` (owner, admin, operator; API keys `webhooks:write`). Writes a `webhookEndpoint.create` " +
			"audit record. Supports `Idempotency-Key`; returns `Location` and `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "webhook.mtls is disabled."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "events must list at least one event type or *."},
			httpapi.ProblemDoc{Code: errs.CodeEndpointURLBlocked, Detail: "The endpoint URL resolves to a private address, which the egress policy blocks."},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError, Detail: "rule.filter: undeclared reference to 'fromDomian'."},
			httpapi.ProblemDoc{Code: errs.CodeEndpointNameTaken, Detail: "A webhook endpoint with this name already exists."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused},
			httpapi.ProblemDoc{Code: errs.CodeQuotaEndpointsExceeded, Detail: "The organisation already has the maximum number of webhook endpoints."}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(createEndpointExamples())},
		Responses:   map[string]*huma.Response{"201": {Content: jsonContent(exampleEndpointView())}},
		Extensions:  httpapi.WithScope(phase, "webhook-endpoints:write"),
	}, m.createEndpoint)
	httpapi.Register(server, huma.Operation{
		OperationID: "getWebhookEndpoint", Method: http.MethodGet, Path: "/v1/webhook-endpoints/{endpointId}",
		Summary: "Get webhook endpoint", Tags: []string{"webhook-endpoints"},
		Description: "Returns one webhook endpoint with secrets redacted, its breaker state, the reason when it was " +
			"disabled automatically and its newest delivery, plus the `ETag` to send as `If-Match` on `PATCH` / " +
			"`DELETE`. An endpoint of another organisation is `404 not_found`. Requires the `read` permission (API " +
			"keys `webhooks:read` or `webhooks:read:<endpointId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleEndpointView())}},
		Extensions: httpapi.WithScope(phase, "webhook-endpoints:read"),
	}, m.getEndpoint)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchWebhookEndpoint", Method: http.MethodPatch, Path: "/v1/webhook-endpoints/{endpointId}",
		Summary: "Update webhook endpoint", Tags: []string{"webhook-endpoints"},
		Description: "Changes the given fields (JSON merge semantics: omitted fields stay, objects are replaced as a " +
			"whole except `delivery`, which merges). Enabling an endpoint clears `disabledReason`. A new rule `filter` / `map` saves a rule version (see …/rule/versions). Send the `ETag` as " +
			"`If-Match` to avoid lost updates (`412 version_mismatch`). Requires `webhooks.write` (API keys " +
			"`webhooks:write` or `webhooks:write:<endpointId>`). Writes a `webhookEndpoint.update` audit record " +
			"(secrets redacted). Returns the endpoint and its new `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "webhook.mtls is disabled."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "A signing secret cannot be empty."},
			httpapi.ProblemDoc{Code: errs.CodeEndpointURLBlocked, Detail: "The endpoint URL resolves to a private address, which the egress policy blocks."},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError, Detail: "rule.map: syntax error at 1:14."},
			httpapi.ProblemDoc{Code: errs.CodeEndpointNameTaken, Detail: "A webhook endpoint with this name already exists."},
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "Webhook endpoint state changed concurrently."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(patchEndpointExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonContent(exampleEndpointView())}},
		Extensions:  httpapi.WithScope(phase, "webhook-endpoints:write"),
	}, m.patchEndpoint)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteWebhookEndpoint", Method: http.MethodDelete, Path: "/v1/webhook-endpoints/{endpointId}",
		Summary: "Delete webhook endpoint", Tags: []string{"webhook-endpoints"},
		Description: "Deletes the endpoint; its delivery records stay until the delivery history retention removes them. " +
			"Routes that target the endpoint must be deleted first (`409 conflict`). Optional " +
			"`If-Match`. Requires `webhooks.write` (API keys `webhooks:write` or `webhooks:write:<endpointId>`). " +
			"Writes a `webhookEndpoint.delete` audit record. Answers `204` without a body.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "Delete routes targeting this endpoint first."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(phase, "webhook-endpoints:write"),
	}, m.deleteEndpoint)
	httpapi.Register(server, huma.Operation{
		OperationID: "rotateWebhookEndpointSecret", Method: http.MethodPost, Path: "/v1/webhook-endpoints/{endpointId}/rotate-secret",
		Summary: "Rotate endpoint secret", Tags: []string{"webhook-endpoints"},
		Description: "Generates a new signing secret and returns it once (`secret`; afterwards only `secretPreview`). " +
			"The previous secret keeps signing next to the new one until `expiresAt` (overlap 24 h, or the " +
			"`webhook.secret.rotationOverlap` switch), so receivers can switch without dropping deliveries: during " +
			"the overlap `webhook-signature` carries one signature per secret. Requires `webhooks.write` (API keys " +
			"`webhooks:write` or `webhooks:write:<endpointId>`). Writes a `webhookEndpoint.rotateSecret` audit " +
			"record (the secret itself is never logged).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "Webhook endpoint state changed concurrently."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{ //nolint:gosec // G101: documentation example rotated secret, not a real credential.
			"secret": "whsec_Zt8mQ1pV7xK3nB9cL5dH2jF6sA0wE4yR8uI1oP3sT6g=", "secretPreview": "whsec_…T6g=",
			"expiresAt": "2026-10-28T09:40:00Z", "id": "3f9a1c0b7e2d4a58"})}},
		Extensions: httpapi.WithScope(phase, "webhook-endpoints:write"),
	}, m.rotateSecret)
	httpapi.Register(server, huma.Operation{
		OperationID: "testWebhookEndpoint", Method: http.MethodPost, Path: "/v1/webhook-endpoints/{endpointId}/test",
		Summary: "Send synthetic endpoint.test delivery", Tags: []string{"webhook-endpoints"},
		Description: "Sends a synthetic `endpoint.test` event to the endpoint right away (signed like any delivery) and " +
			"returns the attempt: status code, latency, response excerpt, or the error kind and code when the request " +
			"failed. The event is not stored and never routed; the delivery appears in the delivery history. A " +
			"disabled endpoint or an open breaker answers `200` with the failed attempt (`endpoint_disabled`, " +
			"`webhook_breaker_open`). Requires `webhooks.write` (API keys `webhooks:write` or " +
			"`webhooks:write:<endpointId>`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleAttemptOK())}},
		Extensions: httpapi.WithScope(phase, "webhook-endpoints:write"),
	}, m.testEndpoint)
	httpapi.Register(server, huma.Operation{
		OperationID: "testWebhookEndpointRule", Method: http.MethodPost, Path: "/v1/webhook-endpoints/{endpointId}/rule/test",
		Summary: "Evaluate the endpoint CEL rule against an event", Tags: []string{"webhook-endpoints"},
		Description: "Evaluates the endpoint's CEL rule (or a draft `filter` / `map` / `builder` sent in the body) " +
			"against one event: an inline CloudEvents envelope (`event`), a stored event (`eventId`) or a built-in " +
			"`sample` of a type. Returns whether it matched, the mapped body, the CEL cost, a per-term trace of the " +
			"filter and, for a draft that does not compile, the error position. Nothing is delivered or saved; a draft " +
			"bypasses the program cache. Requires `webhooks.write` (API keys `webhooks:write` or " +
			"`webhooks:write:<endpointId>`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "event, eventId or sample is required"}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(ruleTestExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonExamples(ruleTestResultExamples())}},
		Extensions:  httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:write"),
	}, m.testEndpointRule)
	httpapi.Register(server, huma.Operation{
		OperationID: "getWebhookEndpointRuleStats", Method: http.MethodGet, Path: "/v1/webhook-endpoints/{endpointId}/rule/stats",
		Summary: "Counters for the endpoint CEL rule", Tags: []string{"webhook-endpoints"},
		Description: "Returns the counters of the endpoint's CEL rule across all nodes: events evaluated, matched, " +
			"skipped and failed, with the time of the last match and the last error. Counters are flushed every " +
			"second, so they can lag slightly. Requires the `read` permission (API keys `webhooks:read` or " +
			"`webhooks:read:<endpointId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."}),
		Extensions: httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:read"),
	}, m.getEndpointRuleStats)
	httpapi.Register(server, huma.Operation{
		OperationID: "listWebhookEndpointRuleDecisions", Method: http.MethodGet, Path: "/v1/webhook-endpoints/{endpointId}/rule/decisions",
		Summary: "Recent filter decisions for the endpoint rule", Tags: []string{"webhook-endpoints"},
		Description: "Lists the most recent filter decisions of the endpoint's rule (at most 50 are kept), newest " +
			"first: the event, whether it matched, the first false term when it did not, or the evaluation error. " +
			"Use it to see why an event was or was not delivered. Requires the `read` permission (API keys " +
			"`webhooks:read` or `webhooks:read:<endpointId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."}),
		Extensions: httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:read"),
	}, m.listEndpointRuleDecisions)
	httpapi.Register(server, huma.Operation{
		OperationID: "dryRunWebhookEndpointRule", Method: http.MethodPost, Path: "/v1/webhook-endpoints/{endpointId}/rule/dry-run",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Dry-run the endpoint rule against recent events", Tags: []string{"webhook-endpoints"},
		Description: "Starts a `rules.dryRun` job that runs the endpoint's rule (or the draft in the body) over the " +
			"stored events of the last `windowHours` (at most 24 h, at most 1000 events) and reports how many matched, " +
			"up to 20 mapped samples, errors per phase and the CEL cost. Nothing is delivered. Answers `202` with the " +
			"job; follow it at `Location` (`GET /v1/jobs/{jobId}`). Requires `webhooks.write` (API keys " +
			"`webhooks:write` or `webhooks:write:<endpointId>`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The endpoint has no filter or map rule; send one in the request to try a draft."},
			httpapi.ProblemDoc{Code: errs.CodeRouteCompileError, Detail: "filter: undeclared reference to 'fromDomian'."}),
		RequestBody: &huma.RequestBody{Content: jsonContent(exampleDryRunRequest())},
		Responses:   map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("rules.dryRun"))}},
		Extensions:  httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:write"),
	}, m.dryRunEndpointRule)
	httpapi.Register(server, huma.Operation{
		OperationID: "listWebhookEndpointRuleVersions", Method: http.MethodGet, Path: "/v1/webhook-endpoints/{endpointId}/rule/versions",
		Summary: "List endpoint rule expression versions", Tags: []string{"webhook-endpoints"},
		Description: "Lists the saved versions of the endpoint's rule (filter, map, builder), newest first; every " +
			"change of the rule saves one. Restore one with …/rule/versions/{version}/restore. Paged with `limit` " +
			"(default 50, max 200) and `page`; `includeTotal` adds the count. Requires the `read` permission (API " +
			"keys `webhooks:read` or `webhooks:read:<endpointId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRuleVersions())}},
		Extensions: httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:read"),
	}, m.listEndpointRuleVersions)
	httpapi.Register(server, huma.Operation{
		OperationID: "restoreWebhookEndpointRuleVersion", Method: http.MethodPost,
		Path:    "/v1/webhook-endpoints/{endpointId}/rule/versions/{version}/restore",
		Summary: "Restore a prior endpoint rule version", Tags: []string{"webhook-endpoints"},
		Description: "Makes a saved rule version the endpoint's current rule again (saved as a new version, so the " +
			"history is kept; the endpoint update validates and compiles it again). Optional `If-Match` with the endpoint's `ETag`. Requires `webhooks.write` (API keys " +
			"`webhooks:write` or `webhooks:write:<endpointId>`). Writes an `endpoint.rule.restore` audit record. " +
			"Returns the endpoint and its new `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Endpoint rule version not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "version must be >= 1"},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleRuleEndpointView())}},
		Extensions: httpapi.WithScope(httpapi.Phase("P3"), "webhook-endpoints:write"),
	}, m.restoreEndpointRuleVersion)
	httpapi.Register(server, huma.Operation{
		OperationID: "listDeliveries", Method: http.MethodGet, Path: "/v1/deliveries",
		Summary: "List deliveries", Tags: []string{"deliveries"},
		Description: "Lists delivery records (one per event and endpoint, with every attempt): state, HTTP status, " +
			"latency, error and a response excerpt. Filter by `endpointId`, `routeId`, `eventId`, `eventType`, " +
			"`accountId`, `state` (one or a comma list, e.g. `failed,deadLettered`) and the creation time range " +
			"`from` / `to`; `q` searches event type and endpoint ID. Records expire after the delivery history " +
			"retention. Pagination: `limit` (default 50, max 200) with `cursor`, or `page` with `includeTotal`; sort " +
			"`id`, `createdAt`, `state` or `lastAttemptAt` (default `-id`). Requires the `read` permission (API keys " +
			"`deliveries:read`; per-endpoint scopes see only those endpoints). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleDeliveryList())}},
		Extensions: httpapi.WithScope(phase, "deliveries:read"),
	}, m.listDeliveries)
	httpapi.Register(server, huma.Operation{
		OperationID: "getDelivery", Method: http.MethodGet, Path: "/v1/deliveries/{deliveryId}",
		Summary: "Get delivery", Tags: []string{"deliveries"},
		Description: "Returns one delivery with all its attempts. Requires the `read` permission (API keys " +
			"`deliveries:read`, or a scope on the delivery's endpoint). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Delivery not found."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleDelivery())}},
		Extensions: httpapi.WithScope(phase, "deliveries:read"),
	}, m.getDelivery)
	httpapi.Register(server, huma.Operation{
		OperationID: "replayDelivery", Method: http.MethodPost, Path: "/v1/deliveries/{deliveryId}/replay",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Replay one delivery", Tags: []string{"deliveries"},
		Description: "Queues a new delivery of the same event to the same endpoint (`replayOf` points to the original, " +
			"which gets `replayedAt`). Only final deliveries (succeeded, failed, deadLettered, cancelled) can be " +
			"replayed, and only while the event is still stored (`422 replay_window_exceeded`). The replay carries " +
			"the `eb-replay: true` header. Answers `202` with the new delivery and its `Location`. Requires " +
			"`webhooks.write` on the endpoint (API keys `webhooks:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The delivery does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "Only a final delivery can be replayed."},
			httpapi.ProblemDoc{Code: errs.CodeReplayWindowExceeded, Detail: "The original event is outside the replay window."}),
		Responses:  map[string]*huma.Response{"202": {Content: jsonContent(exampleReplayDelivery())}},
		Extensions: httpapi.WithScope(phase, "deliveries:write"),
	}, m.replayDelivery)
	httpapi.Register(server, huma.Operation{
		OperationID: "bulkReplayDeliveries", Method: http.MethodPost, Path: "/v1/deliveries/replay",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Replay many deliveries by filter", Tags: []string{"deliveries"},
		Description: "Starts a `deliveries.replay` job that replays every final delivery matching the filter (or the " +
			"listed `ids`, at most 1000) like POST /v1/deliveries/{deliveryId}/replay. At least one filter is " +
			"required, so a request can never replay everything; `state` may only name final states and defaults " +
			"to all of them; already replayed deliveries are skipped unless `includeReplayed`. The job result counts " +
			"replayed, skipped and failed deliveries. Answers `202` with the job and its `Location`. Requires " +
			"`webhooks.write` (API keys `webhooks:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "At least one filter or ids is required; refusing to replay everything."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(bulkReplayExamples())},
		Responses:   map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("deliveries.replay"))}},
		Extensions:  httpapi.WithScope(httpapi.Phase("P3"), "deliveries:write"),
	}, m.bulkReplayDeliveries)
	httpapi.Register(server, huma.Operation{
		OperationID: "replayEventsToEndpoint", Method: http.MethodPost, Path: "/v1/events/replay",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Point-in-time replay of stored events to an endpoint", Tags: []string{"events"},
		Description: "Starts an `events.replay` job that delivers the stored events of a time range to one endpoint " +
			"again, e.g. after the receiver lost data: `from` (required, within the event retention) to `to` " +
			"(default now), optionally only some `types`. By default the endpoint's subscription and account/folder " +
			"filter apply; `ignoreEndpointFilter` sends the events the filter would skip too. Every delivery carries " +
			"`eb-replay: true`; receivers deduplicate by event `id`. Answers `202` with the job and its `Location`. " +
			"Requires `webhooks.write` (API keys `webhooks:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Webhook endpoint not found."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "from is required."},
			httpapi.ProblemDoc{Code: errs.CodeReplayWindowExceeded, Detail: "from is outside the event retention window."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonContent(exampleEventsReplay())},
		Responses:   map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("events.replay"))}},
		Extensions:  httpapi.WithScope(httpapi.Phase("P3"), "deliveries:write"),
	}, m.replayEventsToEndpoint)
}

type endpointListInput struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6IndoZV8wMW01Y3NoeWMwZDIzeGtocmJ6N2U0OTBwbSJ9"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"id, name or createdAt; prefix - for descending. Default -id." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive substring of the name or URL." example:"crm"`
}

type endpointListOutput struct {
	Body EndpointList
}

type endpointCreateInput struct {
	Body EndpointCreate
}

type endpointCreateOutput struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new endpoint." example:"/v1/webhook-endpoints/whe_01m5cshyc0d23xkhrbz7e490pm"`
	ETag     string `header:"ETag"`
	Body     EndpointView
}

type endpointOutput struct {
	ETag string `header:"ETag"`
	Body EndpointView
}

type endpointIDInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
}

type endpointPatchInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	IfMatch    string `header:"If-Match"`
	Body       EndpointUpdate
}

type endpointDeleteInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	IfMatch    string `header:"If-Match"`
}

type rotateOutput struct {
	Body RotateSecretResult
}

type attemptOutput struct {
	Body DeliveryAttempt
}

type deliveryListInput struct {
	Limit        int       `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string    `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImRsdl8wMW01ZHZkeHcwajdhNTV0amF2bTFmbWhleCJ9"`
	Page         int       `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool      `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string    `query:"sort" doc:"id, createdAt, state or lastAttemptAt; prefix - for descending. Default -id." example:"-createdAt"`
	Q            string    `query:"q" doc:"Case-insensitive substring of the event type or endpoint ID." example:"message."`
	EndpointID   string    `query:"endpointId" doc:"Only deliveries to this endpoint (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	RouteID      string    `query:"routeId" doc:"Only deliveries of this route (rte_…)." example:"rte_01m5dafy40qhsm3b2qx0ef6skh"`
	EventID      string    `query:"eventId" doc:"Only deliveries of this event (evt_…)." example:"evt_01m5czx69032gsbkp82n4ajd1w"`
	EventType    string    `query:"eventType" doc:"Only this event type." example:"message.received"`
	AccountID    string    `query:"accountId" doc:"Only this account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	State        string    `query:"state" doc:"Only this state, or a comma list (e.g. failed,deadLettered)." example:"failed,deadLettered"`
	From         time.Time `query:"from" doc:"Created at or after (RFC 3339)." example:"2026-10-27T00:00:00Z"`
	To           time.Time `query:"to" doc:"Created at or before (RFC 3339)." example:"2026-10-27T23:59:59Z"`
}

type deliveryListOutput struct {
	Body DeliveryList
}

type deliveryIDInput struct {
	DeliveryID string `path:"deliveryId" doc:"Delivery ID (dlv_…)." example:"dlv_01m5dvdxw0j7a55tjavm1fmhex"`
}

type deliveryOutput struct {
	Body Delivery
}

type deliveryAcceptedOutput struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new (replay) delivery." example:"/v1/deliveries/dlv_01m5e1s5s0fzzsp861d0hb22qd"`
	Body     Delivery
}

type bulkReplayInput struct {
	Body BulkReplayFilter
}

type jobAcceptedBody struct {
	Job any `json:"job" doc:"The queued job (GET /v1/jobs/{jobId} to follow it)"`
}

type jobAcceptedOutput struct {
	Status   int
	Location string `header:"Location" doc:"URL of the job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     jobAcceptedBody
}

func (m *Module) listEndpoints(ctx context.Context, input *endpointListInput) (*endpointListOutput, error) {
	result, err := m.service.ListEndpoints(ctx, EndpointListFilter{
		Limit: input.Limit, Cursor: input.Cursor, Page: input.Page,
		IncludeTotal: input.IncludeTotal, Sort: input.Sort, Q: input.Q,
	})
	if err != nil {
		return nil, err
	}
	return &endpointListOutput{Body: *result}, nil
}

func (m *Module) createEndpoint(ctx context.Context, input *endpointCreateInput) (*endpointCreateOutput, error) {
	endpoint, err := m.service.CreateEndpoint(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	return &endpointCreateOutput{
		Status: http.StatusCreated, Location: "/v1/webhook-endpoints/" + endpoint.ID,
		ETag: formatETag(endpoint.Version), Body: m.service.ViewEndpoint(ctx, endpoint),
	}, nil
}

func (m *Module) getEndpoint(ctx context.Context, input *endpointIDInput) (*endpointOutput, error) {
	endpoint, err := m.service.GetEndpoint(ctx, input.EndpointID)
	if err != nil {
		return nil, err
	}
	return &endpointOutput{ETag: formatETag(endpoint.Version), Body: m.service.ViewEndpoint(ctx, endpoint)}, nil
}

func (m *Module) patchEndpoint(ctx context.Context, input *endpointPatchInput) (*endpointOutput, error) {
	endpoint, err := m.service.UpdateEndpoint(ctx, input.EndpointID, input.Body, input.IfMatch)
	if err != nil {
		return nil, err
	}
	return &endpointOutput{ETag: formatETag(endpoint.Version), Body: m.service.ViewEndpoint(ctx, endpoint)}, nil
}

func (m *Module) deleteEndpoint(ctx context.Context, input *endpointDeleteInput) (*struct{}, error) {
	if err := m.service.DeleteEndpoint(ctx, input.EndpointID, input.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) rotateSecret(ctx context.Context, input *endpointIDInput) (*rotateOutput, error) {
	result, err := m.service.RotateSecret(ctx, input.EndpointID)
	if err != nil {
		return nil, err
	}
	return &rotateOutput{Body: *result}, nil
}

func (m *Module) testEndpoint(ctx context.Context, input *endpointIDInput) (*attemptOutput, error) {
	result, err := m.service.TestEndpoint(ctx, input.EndpointID)
	if err != nil {
		return nil, err
	}
	return &attemptOutput{Body: *result}, nil
}

type endpointRuleTestInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Body       rules.RouteTestRequest
}

type endpointRuleTestOutput struct {
	Body rules.RouteTestResult
}

func (m *Module) testEndpointRule(ctx context.Context, input *endpointRuleTestInput) (*endpointRuleTestOutput, error) {
	res, err := m.service.TestEndpointRule(ctx, input.EndpointID, input.Body)
	if err != nil {
		return nil, err
	}
	return &endpointRuleTestOutput{Body: *res}, nil
}

type endpointRuleStatsOutput struct {
	Body rules.RuleStats
}

func (m *Module) getEndpointRuleStats(ctx context.Context, input *endpointIDInput) (*endpointRuleStatsOutput, error) {
	st, err := m.service.EndpointRuleStats(ctx, input.EndpointID)
	if err != nil {
		return nil, err
	}
	return &endpointRuleStatsOutput{Body: *st}, nil
}

type endpointRuleDecisionsInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Limit      int    `query:"limit" doc:"Number of decisions (default and at most 50)." example:"20"`
}

type endpointRuleDecisionsOutput struct {
	Body rules.RuleDecisionList
}

func (m *Module) listEndpointRuleDecisions(ctx context.Context, input *endpointRuleDecisionsInput) (*endpointRuleDecisionsOutput, error) {
	res, err := m.service.EndpointRuleDecisions(ctx, input.EndpointID, input.Limit)
	if err != nil {
		return nil, err
	}
	return &endpointRuleDecisionsOutput{Body: *res}, nil
}

func (m *Module) listDeliveries(ctx context.Context, input *deliveryListInput) (*deliveryListOutput, error) {
	result, err := m.service.ListDeliveries(ctx, DeliveryFilter{
		Limit: input.Limit, Cursor: input.Cursor, Page: input.Page, IncludeTotal: input.IncludeTotal,
		Sort: input.Sort, Q: input.Q, EndpointID: input.EndpointID, RouteID: input.RouteID, EventID: input.EventID,
		EventType: input.EventType, AccountID: input.AccountID, State: input.State, From: input.From, To: input.To,
	})
	if err != nil {
		return nil, err
	}
	return &deliveryListOutput{Body: *result}, nil
}

func (m *Module) getDelivery(ctx context.Context, input *deliveryIDInput) (*deliveryOutput, error) {
	delivery, err := m.service.GetDelivery(ctx, input.DeliveryID)
	if err != nil {
		return nil, err
	}
	return &deliveryOutput{Body: *delivery}, nil
}

func (m *Module) replayDelivery(ctx context.Context, input *deliveryIDInput) (*deliveryAcceptedOutput, error) {
	delivery, err := m.service.Replay(ctx, input.DeliveryID)
	if err != nil {
		return nil, err
	}
	return &deliveryAcceptedOutput{
		Status: http.StatusAccepted, Location: "/v1/deliveries/" + delivery.ID, Body: *delivery,
	}, nil
}

func (m *Module) bulkReplayDeliveries(ctx context.Context, input *bulkReplayInput) (*jobAcceptedOutput, error) {
	job, err := m.service.BulkReplay(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	return &jobAcceptedOutput{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID,
		Body: jobAcceptedBody{Job: job},
	}, nil
}

type eventsReplayInput struct {
	Body EventsReplayRequest
}

func (m *Module) replayEventsToEndpoint(ctx context.Context, input *eventsReplayInput) (*jobAcceptedOutput, error) {
	job, err := m.service.ReplayEvents(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	return &jobAcceptedOutput{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID,
		Body: jobAcceptedBody{Job: job},
	}, nil
}

type endpointRuleDryRunInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Body       rules.DryRunRequest
}

type endpointRuleDryRunOutput struct {
	Status   int
	Location string `header:"Location" doc:"URL of the job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     struct {
		Job any `json:"job" doc:"The queued rules.dryRun job (GET /v1/jobs/{jobId} to follow it)"`
	}
}

func (m *Module) dryRunEndpointRule(ctx context.Context, input *endpointRuleDryRunInput) (*endpointRuleDryRunOutput, error) {
	job, err := m.service.DryRunEndpointRule(ctx, input.EndpointID, input.Body)
	if err != nil {
		return nil, err
	}
	out := &endpointRuleDryRunOutput{Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID}
	out.Body.Job = job
	return out, nil
}

type endpointRuleVersionsInput struct {
	EndpointID   string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Limit        int    `query:"limit" minimum:"0" maximum:"200" doc:"Page size (default 50, at most 200)." example:"50"`
	Page         int    `query:"page" minimum:"0" doc:"1-based page number." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total." example:"true"`
}

type endpointRuleVersionsOutput struct {
	Body EndpointRuleVersionList
}

func (m *Module) listEndpointRuleVersions(ctx context.Context, input *endpointRuleVersionsInput) (*endpointRuleVersionsOutput, error) {
	res, err := m.service.ListEndpointRuleVersions(ctx, input.EndpointID, input.Limit, input.Page, input.IncludeTotal)
	if err != nil {
		return nil, err
	}
	return &endpointRuleVersionsOutput{Body: *res}, nil
}

type endpointRuleRestoreInput struct {
	EndpointID string `path:"endpointId" doc:"Webhook endpoint ID (whe_…)." example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Version    int    `path:"version" doc:"Rule version number to restore." example:"1"`
	IfMatch    string `header:"If-Match"`
}

func (m *Module) restoreEndpointRuleVersion(ctx context.Context, input *endpointRuleRestoreInput) (*endpointOutput, error) {
	endpoint, err := m.service.RestoreEndpointRuleVersion(ctx, input.EndpointID, input.Version, input.IfMatch)
	if err != nil {
		return nil, err
	}
	return &endpointOutput{ETag: formatETag(endpoint.Version), Body: m.service.ViewEndpoint(ctx, endpoint)}, nil
}
