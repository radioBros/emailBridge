package submit

import (
	"context"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P1")
	// The body is Accepted (202) or, for a dry run, DryRunResult (200); declare both for the document.
	reg := server.API.OpenAPI().Components.Schemas
	jsonOf := func(t reflect.Type, name string, ex map[string]*huma.Example) map[string]*huma.MediaType {
		return map[string]*huma.MediaType{"application/json": {Schema: reg.Schema(t, true, name), Examples: ex}}
	}
	const writePerm = "Requires `outbound.write` (owner, admin, operator; API keys `outbound:write`). "
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated},
		{Code: errs.CodeForbidden, Detail: "Your role in this organisation does not allow this action."},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the outbound:write scope, or is bound to another account."}}
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, auth...), extra...)...)
	}
	notFound := httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The outbound message does not exist."}
	versionMismatch := httpapi.ProblemDoc{Code: errs.CodeVersionMismatch, Detail: "The resource version does not match If-Match."}
	httpapi.Register(server, huma.Operation{
		OperationID: "submitOutboundMessage", Method: http.MethodPost,
		Path:    "/v1/accounts/{accountId}/outbound-messages",
		Summary: "Submit a message", Tags: []string{"outbound-messages"},
		Description: "Queues one message for sending through the account's SMTP server (or its gateway / Gmail / Graph " +
			"API) and answers `202` with the outbound message ID and `Location`; follow it with `GET " +
			"/v1/outbound-messages/{outboundId}` or the `message.sent` / `message.failed` / `message.bounced` " +
			"events. The body is either structured fields (`from` — default the account address —, `to` / `cc` / " +
			"`bcc`, `subject`, `text`, `html`, `headers`, base64 `attachments`) or a complete base64 RFC 822 `raw` " +
			"message (optionally with an SMTP `envelope`; both at once is `422 raw_and_fields`); or a stored " +
			"`template` with variables; `recipients` sends a mail merge (one message per entry, up to 100, answered " +
			"as `{ \"messages\": [ … ] }`); `reference` replies to or forwards an indexed message. `sendAt` in the " +
			"future schedules it (`state: scheduled`); `policy: atMostOnce` never resends after an unknown outcome. " +
			"`dryRun: true` builds and validates without storing or sending and answers `200` (with `includeRaw=true` " +
			"also the built message; a merge dry run answers `{ \"results\": [ … ] }`). Recipients on a suppression " +
			"list are refused (`422 recipient_suppressed`), the send quotas apply (`429` with `Retry-After`), the " +
			"size limit is `EB_SUBMIT_MAX_MESSAGE_SIZE` (25 MiB). Send `Idempotency-Key` to make retries safe: the " +
			"same key and body replays the first answer with `Idempotent-Replayed: true`. " + writePerm +
			"An account-bound key only for its account.",
		Metadata: problems(
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeAccountDisabled, Detail: "The account is disabled; resume it before sending."},
			httpapi.ProblemDoc{Code: errs.CodeAccountDeleting, Detail: "The account is being deleted."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused, Detail: "This Idempotency-Key was used with a different request body."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyInProgress, Detail: "A request with this Idempotency-Key is still running."},
			httpapi.ProblemDoc{Code: errs.CodeMessageTooLarge, Detail: "The message is 31457280 bytes; the limit is 26214400 bytes."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "A message needs raw or the structured fields."},
			httpapi.ProblemDoc{Code: errs.CodeNoRecipients, Detail: "at least one recipient is required"},
			httpapi.ProblemDoc{Code: errs.CodeInvalidAddress, Detail: "mail: missing '@' or angle-addr"},
			httpapi.ProblemDoc{Code: errs.CodeRawAndFields, Detail: "Send either raw or the structured fields, not both."},
			httpapi.ProblemDoc{Code: errs.CodeRecipientSuppressed, Detail: "1 recipient(s) are on a suppression list."},
			httpapi.ProblemDoc{Code: errs.CodeMailMergeTooLarge, Detail: "Mail merge allows at most 100 recipients."},
			httpapi.ProblemDoc{Code: errs.CodeReferenceInvalid, Detail: "reference.action must be reply, replyAll or forward."},
			httpapi.ProblemDoc{Code: errs.CodeTemplateNotFound, Detail: "The template does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeTemplateVariableMissing, Detail: "A required template variable is missing."},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "sending.templates is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeQuotaSendsExceeded, Detail: "The account reached its per-minute send quota."},
			httpapi.ProblemDoc{Code: errs.CodeOrganizationQuotaExceeded, Detail: "The organisation's sendsPerDay quota (5000) is reached."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
		RequestBody:   &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Examples: exampleSubmitRequests()}}},
		Extensions:    httpapi.WithScope(phase, "outbound-messages:write"),
		DefaultStatus: http.StatusAccepted,
		Responses: map[string]*huma.Response{
			"202": {Description: "Accepted: the message is queued for sending.", Content: jsonOf(reflect.TypeFor[Accepted](), "Accepted", map[string]*huma.Example{
				"queued": {Summary: "Queued for sending now", Value: map[string]any{
					"id": exampleOutboundID, "messageId": "<20261027101500.1@example.com>", "state": "queued",
				}},
				"scheduled": {Summary: "Scheduled with sendAt", Value: map[string]any{
					"id": "out_01m5dvdxw09qnk5y0h3w3w2z3d", "messageId": "<20261027101502.2@example.com>",
					"state": "scheduled", "sendAt": "2026-10-28T08:00:00Z",
				}},
			})},
			"200": {Description: "Dry run: the message was built and validated; nothing was stored or sent.", Content: jsonOf(reflect.TypeFor[DryRunResult](), "DryRunResult", map[string]*huma.Example{
				"dryRun": {Summary: "Dry run", Value: map[string]any{
					"messageId": "<20261027101500.1@example.com>", "size": 2187,
					"envelope": map[string]any{"from": "billing@example.com", "to": []any{"anna.rossi@example.org"}, "cc": []any{"accounts@example.org"}, "bccCount": 1},
				}},
				"dryRunRaw": {Summary: "Dry run with includeRaw=true", Value: map[string]any{
					"messageId": "<20261027101500.1@example.com>", "size": 214,
					"envelope": map[string]any{"from": "billing@example.com", "to": []any{"anna.rossi@example.org"}},
					"raw":      "RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg==",
				}},
			})},
		},
	}, m.submit)
	httpapi.Register(server, huma.Operation{
		OperationID: "listOutboundMessages", Method: http.MethodGet, Path: "/v1/outbound-messages",
		Summary: "List outbound messages", Tags: []string{"outbound-messages"},
		Description: "Lists the active organisation's outbound messages, newest first by default (`-id`). Filters: `accountId`, " +
			"`state`, `messageId`, envelope addresses `from` / `to`, and the creation window `createdFrom` / " +
			"`createdTo` (RFC 3339; `createdTo` exclusive and after `createdFrom`, else `422`). Paged by `cursor` or " +
			"`page` (+ `includeTotal`), `limit` 1–200 (default 50); `sort` by `createdAt`, `sendAt`, `state` or `id` " +
			"(prefix `-` for descending). API keys limited to some accounts see only those. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed, errs.CodeSortNotAllowed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleOutboundList())}},
		Extensions: httpapi.WithScope(phase, "outbound-messages:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "getOutboundMessage", Method: http.MethodGet, Path: "/v1/outbound-messages/{outboundId}",
		Summary: "Get outbound message", Tags: []string{"outbound-messages"},
		Description: "Returns one outbound message: its `state` (scheduled, queued, sending, sent, failed, " +
			"outcomeUnknown, cancelled, captured), the SMTP envelope (Bcc only as a count), every delivery attempt with " +
			"the stage and the redacted server reply, the next attempt time, the copy-to-Sent state, the final problem " +
			"of a failed message and, for PEC accounts, the receipt status. The `ETag` is needed for cancel, retry and " +
			"send-now. Requires the `read` permission (every role; API keys `outbound:read`; account-limited keys only " +
			"for their accounts). Read-only.",
		Metadata:   problems(notFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleOutboundSent())}},
		Extensions: httpapi.WithScope(phase, "outbound-messages:read"),
	}, m.get)
	httpapi.Register(server, huma.Operation{
		OperationID: "cancelOutboundMessage", Method: http.MethodDelete, Path: "/v1/outbound-messages/{outboundId}",
		Summary: "Cancel outbound message", Tags: []string{"outbound-messages"},
		Description: "Cancels a `scheduled` or `queued` message (also one waiting for a retry): it becomes `cancelled` " +
			"and is never sent; the answer is the updated message. A message being transmitted (`sending`) or " +
			"already final is `409 outbound_not_cancellable`. `If-Match` is optional (`412 version_mismatch`). " +
			writePerm + "An account-bound key only for its account.",
		Metadata: problems(notFound, versionMismatch,
			httpapi.ProblemDoc{Code: errs.CodeOutboundNotCancellable, Detail: "Only scheduled or queued messages can be cancelled."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			v := exampleOutboundScheduled()
			v["state"] = "cancelled"
			v["version"] = 2
			delete(v, "nextAttemptAt")
			return v
		}())}},
		Extensions: httpapi.WithScope(phase, "outbound-messages:write"),
	}, m.cancelMessage)
	p5 := httpapi.Phase("P5")
	httpapi.Register(server, huma.Operation{
		OperationID: "retryOutboundMessage", Method: http.MethodPost, Path: "/v1/outbound-messages/{outboundId}/retry",
		Summary: "Retry a failed outbound message", Tags: []string{"outbound-messages"},
		Description: "Re-queues a `failed` or `outcomeUnknown` message for another attempt now (the final error is " +
			"cleared; the attempts so far are kept), or makes a queued message waiting for its next retry due at " +
			"once. Other states are `409 outbound_not_retryable`. Beware: an `outcomeUnknown` message may already " +
			"have been delivered — retrying it can send it twice. `If-Match` is optional. " + writePerm +
			"Writes an `outbound.retried` audit record.",
		Metadata: problems(notFound, versionMismatch,
			httpapi.ProblemDoc{Code: errs.CodeOutboundNotRetryable, Detail: "Only failed or outcome-unknown messages can be retried."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			v := exampleOutboundFailed()
			v["state"] = "queued"
			v["nextAttemptAt"] = "2026-10-27T12:30:00Z"
			v["version"] = 8
			delete(v, "finalError")
			return v
		}())}},
		Extensions: httpapi.WithScope(p5, "outbound-messages:write"),
	}, m.retryMessage)
	httpapi.Register(server, huma.Operation{
		OperationID: "sendOutboundMessageNow", Method: http.MethodPost, Path: "/v1/outbound-messages/{outboundId}/send-now",
		Summary: "Send a scheduled or delayed message now", Tags: []string{"outbound-messages"},
		Description: "Makes a `scheduled` message (its `sendAt` becomes now) or a queued message waiting for a retry due " +
			"immediately. A message already due is returned unchanged; other states are `409 outbound_not_sendable`. " +
			"Send windows and throttles still apply when the worker picks it up. `If-Match` is optional. " + writePerm +
			"Writes an `outbound.send_now` audit record.",
		Metadata: problems(notFound, versionMismatch,
			httpapi.ProblemDoc{Code: errs.CodeOutboundNotSendable, Detail: "Only scheduled or delayed queued messages can be sent now."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			v := exampleOutboundScheduled()
			v["state"] = "queued"
			v["sendAt"] = "2026-10-27T12:30:00Z"
			v["nextAttemptAt"] = "2026-10-27T12:30:00Z"
			v["version"] = 2
			return v
		}())}},
		Extensions: httpapi.WithScope(p5, "outbound-messages:write"),
	}, m.sendNowMessage)
}

type submitInput struct {
	AccountID string `path:"accountId" doc:"Sending account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	// IdempotencyKey replays the stored response of an identical request (S2 §2).
	IdempotencyKey string `header:"Idempotency-Key"`
	IncludeRaw     bool   `query:"includeRaw" doc:"Include the built message in a dry-run result" example:"false"`
	Body           SubmitRequest
}

// submitOutput is both answers of a submit: 200 with the dry-run result, 202 with the acceptance. huma
// omits a header whose field is empty, so a dry run answers without Location.
type submitOutput struct {
	Status             int
	Location           string `header:"Location" doc:"URL of the (first) outbound message; absent for a dry run." example:"/v1/outbound-messages/out_01m5dsa5x0s3ggtntesyf0cb50"`
	IdempotentReplayed string `header:"Idempotent-Replayed"`
	Body               any
}

type listInput struct {
	Limit        int    `query:"limit" doc:"Page size, 1–200 (default 50)." example:"25"`
	Cursor       string `query:"cursor" doc:"nextCursor of the previous page (keyset paging)." example:"eyJpZCI6Im9tc2dfMDFtNWQ0NHA3MGh3bjN3NzZuM2Z6cjJnYWEiLCJmaWVsZCI6Il9pZCIsImRlc2MiOnRydWV9"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use with includeTotal)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"Sort field createdAt, sendAt, state or id; prefix - for descending (default -id, newest first)." example:"-createdAt"`
	AccountID    string `query:"accountId" doc:"Only messages of this account." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	State        string `query:"state" doc:"Only messages in this state (queued, sending, sent, failed, scheduled, cancelled …)." example:"failed"`
	MessageID    string `query:"messageId" doc:"Only the message with this Message-ID header." example:"<20261027101500.1@example.com>"`
	From         string `query:"from" doc:"Envelope sender address (exact, case-insensitive)." example:"billing@example.com"`
	To           string `query:"to" doc:"Envelope recipient address (exact)." example:"customer@example.org"`
	CreatedFrom  string `query:"createdFrom" doc:"Only messages created at or after this time (RFC 3339)." example:"2026-10-26T00:00:00Z"`
	CreatedTo    string `query:"createdTo" doc:"Only messages created before this time (RFC 3339, exclusive)." example:"2026-10-27T00:00:00Z"`
}

type listOutput struct {
	Body List
}

type outboundIDInput struct {
	OutboundID string `path:"outboundId" doc:"Outbound message ID (out_…)." example:"out_01m5dsa5x0s3ggtntesyf0cb50"`
}

type cancelInput struct {
	OutboundID string `path:"outboundId" doc:"Outbound message ID (out_…)." example:"out_01m5dsa5x0s3ggtntesyf0cb50"`
	IfMatch    string `header:"If-Match"`
}

type messageOutput struct {
	ETag string `header:"ETag" doc:"Version of the message; send it as If-Match on cancel, retry and send-now." example:"\"v6\""`
	Body View
}

func (m *Module) submit(ctx context.Context, input *submitInput) (*submitOutput, error) {
	res, err := m.service.Submit(ctx, input.AccountID, input.Body, input.IdempotencyKey, input.IncludeRaw)
	if err != nil {
		return nil, err
	}
	out := &submitOutput{Status: res.Status}
	if res.Replayed {
		out.IdempotentReplayed = "true"
	}
	if res.DryRun != nil {
		out.Body = *res.DryRun
		return out, nil
	}
	if res.MergeDryRun != nil {
		out.Body = *res.MergeDryRun
		return out, nil
	}
	if res.MergeAccepted != nil {
		if len(res.MergeAccepted.Messages) > 0 {
			out.Location = "/v1/outbound-messages/" + res.MergeAccepted.Messages[0].ID
		}
		out.Body = *res.MergeAccepted
		return out, nil
	}
	out.Location = "/v1/outbound-messages/" + res.Accepted.ID
	out.Body = *res.Accepted
	return out, nil
}

func (m *Module) list(ctx context.Context, input *listInput) (*listOutput, error) {
	createdFrom, err := parseTimeQuery("createdFrom", input.CreatedFrom)
	if err != nil {
		return nil, err
	}
	createdTo, err := parseTimeQuery("createdTo", input.CreatedTo)
	if err != nil {
		return nil, err
	}
	if createdFrom != nil && createdTo != nil && !createdTo.After(*createdFrom) {
		return nil, errs.New(errs.CodeValidationFailed, "createdTo must be after createdFrom.").WithSafe("param", "createdTo")
	}
	res, err := m.service.List(ctx, ListFilter{
		Limit: input.Limit, Cursor: input.Cursor, Page: input.Page, IncludeTotal: input.IncludeTotal,
		Sort: input.Sort, AccountID: input.AccountID, State: input.State, MessageID: input.MessageID,
		From: input.From, To: input.To, CreatedFrom: createdFrom, CreatedTo: createdTo,
	})
	if err != nil {
		return nil, err
	}
	return &listOutput{Body: *res}, nil
}

func (m *Module) get(ctx context.Context, input *outboundIDInput) (*messageOutput, error) {
	message, err := m.service.Get(ctx, input.OutboundID)
	if err != nil {
		return nil, err
	}
	return &messageOutput{ETag: formatETag(message.Version), Body: ViewOf(message)}, nil
}

func (m *Module) cancelMessage(ctx context.Context, input *cancelInput) (*messageOutput, error) {
	message, err := m.service.Cancel(ctx, input.OutboundID, input.IfMatch)
	if err != nil {
		return nil, err
	}
	return &messageOutput{ETag: formatETag(message.Version), Body: ViewOf(message)}, nil
}

func (m *Module) retryMessage(ctx context.Context, input *cancelInput) (*messageOutput, error) {
	message, err := m.service.Retry(ctx, input.OutboundID, input.IfMatch)
	if err != nil {
		return nil, err
	}
	return &messageOutput{ETag: formatETag(message.Version), Body: ViewOf(message)}, nil
}

func (m *Module) sendNowMessage(ctx context.Context, input *cancelInput) (*messageOutput, error) {
	message, err := m.service.SendNow(ctx, input.OutboundID, input.IfMatch)
	if err != nil {
		return nil, err
	}
	return &messageOutput{ETag: formatETag(message.Version), Body: ViewOf(message)}, nil
}

// parseTimeQuery parses an optional RFC 3339 query parameter; a malformed value is 422 validation_failed.
func parseTimeQuery(name, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, name+" must be an RFC 3339 date-time.").WithSafe("param", name)
	}
	return &t, nil
}
