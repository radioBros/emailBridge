package deliverytest

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P5")
	httpapi.Register(server, huma.Operation{
		OperationID: "createDeliveryTest", Method: http.MethodPost, Path: "/v1/delivery-tests",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Start a delivery test", Tags: []string{"delivery-tests"},
		Description: "Sends a small probe message (subject `EmailBridge delivery test <id>`) from the account to `to` and " +
			"waits until the probe shows up in the same account's index, then reports the folder it landed in (inbox, " +
			"spam, other), the end-to-end latency and the SPF / DKIM / DMARC / ARC results of the received copy. Use an " +
			"address that delivers back into the account. Runs as a `deliveryTest` job: answers `202` with the test ID; " +
			"poll `GET /v1/delivery-tests/{testId}`. The probe is submitted through the account's normal " +
			"outbound queue. In production only the configured test mailbox may be the recipient. " +
			"Requires `outbound.write` (owner, admin, operator; API keys `outbound:write`). Supports `Idempotency-Key`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeNotFound},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "timeout must be at most 10m."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Extensions: httpapi.WithScope(phase, "outbound:write"),
	}, m.create)
	httpapi.Register(server, huma.Operation{
		OperationID: "getDeliveryTest", Method: http.MethodGet, Path: "/v1/delivery-tests/{testId}",
		Summary: "Get delivery test result", Tags: []string{"delivery-tests"},
		Description: "Returns the state and, once finished, the result of a delivery test: `succeeded` with folder, " +
			"mailbox path, latency and authentication results; `failed` or `timedOut` (problem `delivery_test_timeout` " +
			"when the probe did not arrive in time) with the problem in `error`. Requires the `read` permission (API " +
			"keys `outbound:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
			"succeeded": {Summary: "Probe arrived in the inbox", Value: map[string]any{
				"id": "dtt_01m5t8wrq8xkam085mr0twjs74", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
				"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "to": "reception@example.com", "state": "succeeded",
				"outboundId": "out_01m5drrjzg6m505g6fban69akn",
				"messageId":  "<20261027101500.dtt_01m5t8wrq8xkam085mr0twjs74@example.com>",
				"folder":     "inbox", "mailboxPath": "INBOX", "latencyMs": 8421,
				"authResults": map[string]any{
					"spf":    map[string]any{"result": "pass", "domain": "example.com"},
					"dkim":   []any{map[string]any{"result": "pass", "domain": "example.com", "selector": "mail2026"}},
					"dmarc":  map[string]any{"result": "pass", "domain": "example.com"},
					"arc":    map[string]any{"result": "none"},
					"source": "header",
				},
				"jobId": "job_01m5cnaee0w96fc46z350nc6q0", "createdAt": "2026-10-27T10:15:00Z", "finishedAt": "2026-10-27T10:15:08Z",
			}},
			"timedOut": {Summary: "Probe never arrived", Value: map[string]any{
				"id": "dtt_01m5t8wrq8xkam085mr0twjs74", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
				"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "to": "reception@example.com", "state": "timedOut",
				"outboundId": "out_01m5drrjzg6m505g6fban69akn",
				"error": map[string]any{
					"type": "https://emailbridge.dev/problems/delivery_test_timeout", "title": "Delivery test timed out",
					"status": 504, "code": "delivery_test_timeout", "kind": "temporary",
					"detail": "The probe message did not appear in the account index before the timeout.",
				},
				"jobId": "job_01m5cnaee0w96fc46z350nc6q0", "createdAt": "2026-10-27T10:15:00Z", "finishedAt": "2026-10-27T10:17:00Z",
			}},
		}}}}},
		Extensions: httpapi.WithScope(phase, "outbound:read"),
	}, m.get)
}

type createIn struct {
	Body CreateInput
}

type createOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new delivery test." example:"/v1/delivery-tests/dtt_01m5t8wrq8xkam085mr0twjs74"`
	Body     Accepted
}

type idIn struct {
	TestID string `path:"testId" doc:"Delivery test ID (dtt_…)." example:"dtt_01m5t8wrq8xkam085mr0twjs74"`
}

type getOut struct {
	Body View
}

func (m *Module) create(ctx context.Context, in *createIn) (*createOut, error) {
	acc, err := m.svc.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &createOut{
		Status: http.StatusAccepted, Location: "/v1/delivery-tests/" + acc.ID, Body: *acc,
	}, nil
}

func (m *Module) get(ctx context.Context, in *idIn) (*getOut, error) {
	t, err := m.svc.Get(ctx, in.TestID)
	if err != nil {
		return nil, err
	}
	return &getOut{Body: ViewOf(t)}, nil
}
