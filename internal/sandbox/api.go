package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P1")
	const readPerm = "Requires the `read` permission (every role; API keys `sandbox:read`; an account-bound key sees only its account's captures). "
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated},
		{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the scope this operation needs."}}
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, auth...), extra...)...)
	}
	notFound := httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The captured message does not exist."}
	httpapi.Register(server, huma.Operation{
		OperationID: "listSandboxMessages", Method: http.MethodGet, Path: "/v1/sandbox/messages",
		Summary: "List captured sandbox messages", Tags: []string{"sandbox"},
		Description: "Lists the messages the sandbox captured instead of delivering (switch `sandbox.enabled` with " +
			"`sandbox.recipientPolicy` `capture`), newest first by default (`-id`; `sort` also `capturedAt`). Filters: " +
			"`accountId`, the capture window `from` (inclusive) / `to` (exclusive), and `q` (part of the subject, " +
			"case-insensitive). Paged by `cursor` or `page` (+ `includeTotal`, capped at 10 000 with `totalCapped`); " +
			"`limit` 1–200 (default 50). Captures are deleted at `expireAt` (14 days by default). " + readPerm + "Read-only.",
		Metadata: problems(httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The cursor is invalid."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleCaptureList())}},
		Extensions: httpapi.WithScope(phase, "sandbox:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "getSandboxMessage", Method: http.MethodGet, Path: "/v1/sandbox/messages/{captureId}",
		Summary: "Get captured message", Tags: []string{"sandbox"},
		Description: "Returns one captured message: the outbound message it came from, the SMTP envelope it would have " +
			"been sent with (Bcc only as a count), subject and capture / expiry times. The full message is at " +
			"`…/raw`. " + readPerm + "Read-only.",
		Metadata:   problems(notFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleCapture())}},
		Extensions: httpapi.WithScope(phase, "sandbox:read"),
	}, m.get)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteSandboxMessage", Method: http.MethodDelete, Path: "/v1/sandbox/messages/{captureId}",
		Summary: "Delete captured message", Tags: []string{"sandbox"},
		Description: "Deletes the capture and its stored raw copy and answers `204`. Requires `outbound.write` (owner, " +
			"admin, operator; API keys `sandbox:write` or `outbound:write`; an account-bound key only for its account). " +
			"Not reversible.",
		Metadata:   problems(notFound),
		Extensions: httpapi.WithScope(phase, "sandbox:write"),
	}, m.deleteMessage)
	httpapi.Register(server, huma.Operation{
		OperationID: "getSandboxMessageRaw", Method: http.MethodGet, Path: "/v1/sandbox/messages/{captureId}/raw",
		Summary: "Raw captured message", Tags: []string{"sandbox"},
		Description: "Downloads the captured message exactly as it would have been sent (`message/rfc822`, as " +
			"`<captureId>.eml`). A capture without a stored raw copy is `404 not_found`. " + readPerm + "Read-only.",
		Metadata:   problems(notFound),
		Extensions: httpapi.WithScope(phase, "sandbox:read"),
		Responses: map[string]*huma.Response{"200": {Description: "Raw RFC 822",
			Headers: map[string]*huma.Param{"Content-Disposition": {Description: "Download as <captureId>.eml.",
				Schema: &huma.Schema{Type: huma.TypeString}, Example: `attachment; filename="cap_01m5dgv610dh6t59mc4867vabc.eml"`}},
			Content: map[string]*huma.MediaType{"message/rfc822": {
				Schema: &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "The captured message (CRLF line ends)."},
				Example: "From: Billing <billing@example.com>\r\nTo: Anna Rossi <anna.rossi@example.org>\r\n" +
					"Subject: Invoice 2026-1043\r\nMessage-ID: <20261027101500.1@example.com>\r\nMIME-Version: 1.0\r\n" +
					"Content-Type: text/plain; charset=utf-8\r\n\r\nPlease find the invoice attached.\r\n",
			}}}},
	}, m.raw)
}

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleCapture() map[string]any {
	return map[string]any{
		"id": "cap_01m5dgv610dh6t59mc4867vabc", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "outboundId": "out_01m5dgv610f4k7r2t9w3y5z8ab",
		"envelope": map[string]any{
			"from": "billing@example.com", "to": []any{"anna.rossi@example.org"}, "cc": []any{"accounts@example.org"}, "bccCount": 1,
		},
		"subject": "Invoice 2026-1043", "capturedAt": "2026-10-27T10:15:02Z", "expireAt": "2026-11-10T10:15:02Z",
	}
}

func exampleCaptureList() map[string]any {
	second := map[string]any{
		"id": "cap_01m5deqe20ygpqvfw9cjw1chja", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "outboundId": "out_01m5deqe20b6n2c8x4v1m7k3pq",
		"envelope": map[string]any{"from": "noreply@example.com", "to": []any{"marco.bianchi@example.org"}},
		"subject":  "Your password reset code", "capturedAt": "2026-10-27T09:40:11Z", "expireAt": "2026-11-10T09:40:11Z",
	}
	return map[string]any{
		"data":       []any{exampleCapture(), second},
		"nextCursor": "eyJpZCI6ImNhcF8wMW01ZGVxZTIweWdwcXZmdzljancxY2hqYSIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}

type listInput struct {
	Limit        int       `query:"limit" doc:"Page size 1–200 (default 50)." example:"50"`
	Cursor       string    `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImNhcF8wMW01ZGVxZTIweWdwcXZmdzljancxY2hqYSIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int       `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool      `query:"includeTotal" doc:"With page: also return total (capped at 10 000, then totalCapped is true)." example:"true"`
	Sort         string    `query:"sort" doc:"id or capturedAt; prefix - for descending. Default -id (newest first)." example:"-capturedAt"`
	AccountID    string    `query:"accountId" doc:"Only captures of this account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	From         time.Time `query:"from" doc:"Only captures at or after this time (RFC 3339)." example:"2026-10-27T00:00:00Z"`
	To           time.Time `query:"to" doc:"Only captures before this time (RFC 3339, exclusive)." example:"2026-10-28T00:00:00Z"`
	Text         string    `query:"q" doc:"Case-insensitive part of the subject." example:"invoice"`
}

type listOutput struct {
	Body List
}

type captureIDInput struct {
	CaptureID string `path:"captureId" doc:"Capture ID (cap_…)." example:"cap_01m5dgv610dh6t59mc4867vabc"`
}

type captureOutput struct {
	Body View
}

type deleteInput struct {
	CaptureID string `path:"captureId" doc:"Capture ID (cap_…)." example:"cap_01m5dgv610dh6t59mc4867vabc"`
}

func (m *Module) list(ctx context.Context, input *listInput) (*listOutput, error) {
	res, err := m.service.List(ctx, ListFilter{
		Limit: input.Limit, Cursor: input.Cursor, Page: input.Page, IncludeTotal: input.IncludeTotal,
		Sort: input.Sort, AccountID: input.AccountID, From: input.From, To: input.To, Text: input.Text,
	})
	if err != nil {
		return nil, err
	}
	return &listOutput{Body: *res}, nil
}

func (m *Module) get(ctx context.Context, input *captureIDInput) (*captureOutput, error) {
	c, err := m.service.Get(ctx, input.CaptureID)
	if err != nil {
		return nil, err
	}
	return &captureOutput{Body: ViewOf(c)}, nil
}

func (m *Module) deleteMessage(ctx context.Context, input *deleteInput) (*struct{}, error) {
	if err := m.service.Delete(ctx, input.CaptureID); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) raw(ctx context.Context, input *captureIDInput) (*huma.StreamResponse, error) {
	c, body, err := m.service.Raw(ctx, input.CaptureID)
	if err != nil {
		return nil, err
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer func() { _ = body.Close() }()
		hctx.SetHeader("Content-Type", "message/rfc822")
		hctx.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.eml"`, c.ID))
		_, _ = io.Copy(hctx.BodyWriter(), body)
	}}, nil
}
