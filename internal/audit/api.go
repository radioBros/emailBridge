package audit

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

type listInput struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJhdCI6IjIwMjYtMTAtMjRUMTQ6MzI6MTBaIiwiaWQiOiJhdWRfMDFtNWQ0NHA3MGh3bjN3NzZuM2Z6cjJnYWEiLCJkZXNjIjp0cnVlLCJieUF0Ijp0cnVlfQ"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"at or id, prefix - for descending. Default -at (newest first)." example:"-at"`
	ActorID      string `query:"actorId" doc:"Only records by this actor (usr_…, key_… or a system component)." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	TargetID     string `query:"targetId" doc:"Only records about this resource ID." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Action       string `query:"action" doc:"Only this action (exact match)." example:"account.updated"`
	From         string `query:"from" doc:"Only records at or after this time (RFC 3339)." example:"2026-10-01T00:00:00Z"`
	To           string `query:"to" doc:"Only records at or before this time (RFC 3339)." example:"2026-10-27T23:59:59Z"`
}

type listOutput struct {
	Body ListResult
}

func (m *Module) registerAPI(srv *httpapi.Server) {
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAuditLog",
		Method:      http.MethodGet,
		Path:        "/v1/audit-log",
		Summary:     "List audit records",
		Description: "Lists the append-only audit trail of the active organisation: who (user, API key, system) did what " +
			"to which resource, when, from which IP, with field-level before/after values (secrets never stored). " +
			"Requires `audit.read` (owners, admins, operators; API keys need the `audit:read` scope). Instance owners also see " +
			"instance-level records (`organizationId: null`). Filter by actor, target, action and time range; " +
			"pagination with `limit` + `cursor` or `page` + `includeTotal`; sort `at` / `id` (default `-at`). " +
			"Records expire after `EB_RETENTION_AUDIT`. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeSortNotAllowed,
			errs.CodeValidationFailed),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"data": []any{
				map[string]any{
					"id": "aud_01m5d44p70hwn3w76n3fzr2gaa", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "at": "2026-10-24T14:32:10Z",
					"actor":  map[string]any{"type": "user", "id": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "ip": "203.0.113.24"},
					"action": "account.updated", "target": map[string]any{"type": "account", "id": "acc_01m5cvnpb031g57sqct6mzhq94"},
					"changes":   map[string]any{"name": map[string]any{"before": "Front desk", "after": "Reception"}},
					"requestId": "req_01m5d44p70hwn3w76n3fzr2gaa", "traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
				},
				map[string]any{
					"id": "aud_01m5d20y80z8mv4gqjfp3m7140", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "at": "2026-10-24T11:05:47Z",
					"actor":  map[string]any{"type": "apiKey", "id": "key_01m5cqe6d0rs9cyxvy69ynf0t6", "ip": "198.51.100.7"},
					"action": "member.invited", "target": map[string]any{"type": "membership", "id": "mem_01m5ch2yg0fs9wazx4v0ck93t7"},
					"changes": map[string]any{"email": map[string]any{"after": "luca.bianchi@example.com"}, "role": map[string]any{"after": "operator"}},
				},
			},
			"nextCursor": "eyJhdCI6IjIwMjYtMTAtMjRUMTE6MDU6NDdaIiwiaWQiOiJhdWRfMDFtNWQyMHk4MHo4bXY0Z3FqZnAzbTcxNDAiLCJkZXNjIjp0cnVlLCJieUF0Ijp0cnVlfQ",
		}}}}},
		Tags:       []string{"audit-log"},
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "audit:read"),
	}, m.list)
}

func (m *Module) list(ctx context.Context, in *listInput) (*listOutput, error) {
	f := ListFilter{
		ActorID: in.ActorID, TargetID: in.TargetID, Action: in.Action,
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Sort: in.Sort,
	}
	var err error
	if f.From, err = parseTimeParam("from", in.From); err != nil {
		return nil, err
	}
	if f.To, err = parseTimeParam("to", in.To); err != nil {
		return nil, err
	}
	res, err := m.writer.List(ctx, f)
	if err != nil {
		return nil, err
	}
	return &listOutput{Body: *res}, nil
}

// parseTimeParam parses an optional RFC 3339 query parameter; a malformed value is 422 validation_failed.
func parseTimeParam(name, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, name+" must be an RFC 3339 date-time.").WithSafe("param", name)
	}
	return &t, nil
}
