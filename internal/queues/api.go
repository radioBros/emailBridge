package queues

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func registerAPI(srv *httpapi.Server, svc *Service) {
	h := &handlers{svc: svc}
	ext := httpapi.Phase("P7")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listQueueItems", Method: http.MethodGet, Path: "/v1/queues/{queue}/items",
		Summary: "List the records waiting in a queue", Tags: []string{"queues"},
		Description: "Lists the domain records behind a work queue — outbound messages (`EMAILBRIDGE_SUBMIT`), webhook " +
			"deliveries (`EMAILBRIDGE_DELIVERY`) or system mail (`EMAILBRIDGE_SYSMAIL`) — that are scheduled, waiting, " +
			"retry-waiting, dead-lettered or in flight, oldest first. Each item says whether it can be cancelled " +
			"(`cancellable`; an item a worker is transmitting is `inFlight`). Sending needs `read` (API keys " +
			"`outbound:read`), webhook deliveries `read` (`deliveries:read`); account-bound and resource-limited API " +
			"keys see only their accounts / endpoints, and system mail sent through an account is not listed. System " +
			"mail and `scope=all` (every organisation) are for the instance owner only. `EMAILBRIDGE_EVENTS` has no " +
			"item list (`404`). Pagination: `page` (1-based) and `limit` (default 25, max 100), `total` capped at " +
			"10 000; sort by `createdAt` or `nextAttemptAt` (prefix `-` for descending). Read-only.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "Only the instance owner can list system mail or every organisation."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The events queue has no item list; browse events with GET /v1/events."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Unknown state for this queue."},
			httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
		),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExamples(listExamples())}},
		Extensions: httpapi.WithScope(ext, "outbound:read"),
	}, h.list)

	httpapi.Register(srv, huma.Operation{
		OperationID: "cancelQueueItems", Method: http.MethodPost, Path: "/v1/queues/{queue}/items/cancel",
		Summary: "Cancel queued records by id, by filter or the whole queue", Tags: []string{"queues"},
		Description: "Cancels records waiting in a queue by moving each one to its `cancelled` state (the same " +
			"conditional update as `DELETE /v1/outbound-messages/{outboundId}`); the stream is never purged, and a " +
			"worker that later pulls a cancelled record acks it without sending. Records being transmitted right now " +
			"are never cancelled. Send exactly one of: `ids` (1–500, synchronous: `200` with `cancelled` and the " +
			"`skipped` ones with a reason; a single id that cannot be cancelled is `409 queue_item_not_cancellable` " +
			"or `404 not_found`), `filter` (at least one criterion) or `all: true` (the whole queue in the scope, " +
			"`confirm` must equal the queue name). Filter and all count the matching records and start a " +
			"`queues.cancel` job (`202` with `job` and `matched`; poll `GET /v1/jobs/{jobId}`); records queued after " +
			"the request are never touched, and no job is created when nothing matches (`200`, `matched: 0`). " +
			"Sending needs `outbound.write` (owner, admin, operator; API keys `outbound:write`), webhook deliveries " +
			"`webhooks.write` (`webhooks:write`); the caller's account / endpoint limits are kept by the job. System " +
			"mail and `scope: all` (every organisation) are instance-owner only. `EMAILBRIDGE_EVENTS` is read-only " +
			"(`409 queue_read_only`). Emits no event; writes one audit entry per request and a `queues.updates` " +
			"realtime message per request or job batch.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "Your role in this organisation does not allow cancelling outbound messages."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The record does not exist in this queue."},
			httpapi.ProblemDoc{Code: errs.CodeQueueItemNotCancellable, Detail: "The record is being transmitted right now and can no longer be cancelled."},
			httpapi.ProblemDoc{Code: errs.CodeQueueReadOnly, Detail: "Events are facts and are never cancelled; pause the queue on the Cluster screen instead."},
			httpapi.ProblemDoc{Code: errs.CodeQueueConfirmationRequired, Detail: "To cancel the whole queue, set confirm to the queue name."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Send exactly one of ids, filter or all."},
		),
		RequestBody: &huma.RequestBody{Content: jsonExamples(cancelRequestExamples())},
		Responses: map[string]*huma.Response{
			"200": {Description: "Cancelled by ids, or nothing matched the filter", Content: jsonExamples(cancelSyncExamples())},
			"202": {Description: "A queues.cancel job was started", Content: jsonContent(cancelJobExample())},
		},
		Extensions: httpapi.WithScope(ext, "outbound:write"),
	}, h.cancel)
}

type handlers struct{ svc *Service }

type listIn struct {
	Queue       string    `path:"queue" minLength:"1" maxLength:"64" doc:"Queue (stream name): EMAILBRIDGE_SUBMIT, EMAILBRIDGE_DELIVERY or EMAILBRIDGE_SYSMAIL." example:"EMAILBRIDGE_SUBMIT"`
	Scope       string    `query:"scope" enum:"organization,all" default:"organization" doc:"organization (the active one) or all organisations (instance owner)." example:"organization"`
	Page        int       `query:"page" minimum:"1" default:"1" doc:"1-based page number." example:"1"`
	Limit       int       `query:"limit" minimum:"1" maximum:"100" default:"25" doc:"Page size." example:"25"`
	Sort        string    `query:"sort" enum:"createdAt,-createdAt,nextAttemptAt,-nextAttemptAt" default:"createdAt" doc:"Sort field; prefix - for descending." example:"createdAt"`
	State       string    `query:"state" enum:"scheduled,queued,retrying,sending,pending,deadLettered,delivering" doc:"Only one queue state (must belong to the queue)." example:"retrying"`
	AccountID   string    `query:"accountId" maxLength:"64" doc:"Only this account (Sending, webhook deliveries)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	EndpointID  string    `query:"endpointId" maxLength:"64" doc:"Only this webhook endpoint (webhook deliveries)." example:"whe_01m5d9x2r0f4y8j6k3c1v7q2pn"`
	CreatedFrom time.Time `query:"createdFrom" doc:"Queued at or after (RFC 3339)." example:"2026-10-27T00:00:00Z"`
	CreatedTo   time.Time `query:"createdTo" doc:"Queued before (RFC 3339, exclusive)." example:"2026-10-27T12:00:00Z"`
	Q           string    `query:"q" maxLength:"200" doc:"Case-insensitive prefix of the recipient or subject (Sending), event type (webhook deliveries) or recipient (system mail)." example:"anna"`
}

type listOut struct {
	Body *Page
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (h *handlers) list(ctx context.Context, in *listIn) (*listOut, error) {
	page, err := h.svc.List(ctx, in.Queue, ListQuery{
		Filter: Filter{
			State: in.State, AccountID: in.AccountID, EndpointID: in.EndpointID,
			CreatedFrom: timePtr(in.CreatedFrom), CreatedTo: timePtr(in.CreatedTo), Q: in.Q,
		},
		All: in.Scope == ScopeAll, Page: in.Page, Limit: in.Limit, Sort: in.Sort,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: page}, nil
}

type cancelIn struct {
	Queue string `path:"queue" minLength:"1" maxLength:"64" doc:"Queue (stream name): EMAILBRIDGE_SUBMIT, EMAILBRIDGE_DELIVERY or EMAILBRIDGE_SYSMAIL." example:"EMAILBRIDGE_SUBMIT"`
	Body  CancelRequest
}

type cancelOut struct {
	Status int
	Body   *CancelResult
}

func (h *handlers) cancel(ctx context.Context, in *cancelIn) (*cancelOut, error) {
	res, async, err := h.svc.Cancel(ctx, in.Queue, in.Body)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if async {
		status = http.StatusAccepted
	}
	return &cancelOut{Status: status, Body: res}, nil
}
