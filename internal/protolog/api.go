package protolog

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/query"
)

// AccountGate verifies the caller may read an account and returns its organisation ID.
// Implemented by accounts.Service.ProtocolLogAccount (wired from cmd) — kept as an interface so this
// package does not import accounts (smtpc → protolog would otherwise cycle).
type AccountGate interface {
	ProtocolLogAccount(ctx context.Context, accountID string) (organizationID string, err error)
}

// DownloadAuditor records that protocol logs were downloaded (wired to audit.Writer from cmd).
type DownloadAuditor interface {
	ProtocolLogDownload(ctx context.Context, accountID string, from, to time.Time) error
}

// API serves GET /v1/accounts/{accountId}/protocol-logs and …/download.
type API struct {
	Accounts AccountGate
	Logs     *query.Service
	Audit    DownloadAuditor
	Log      *slog.Logger
}

const (
	defaultLimit = 50
	maxLimit     = 200
	defaultSpan  = 7 * 24 * time.Hour
)

func (a *API) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

// Register adds the protocol-logs operations to srv.
func (a *API) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P1")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listProtocolLogs", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/protocol-logs",
		Summary: "Protocol logs", Tags: []string{"accounts"},
		Description: "Returns the IMAP and SMTP transcript of an account, oldest line first: every command " +
			"EmailBridge sent (`dir: C`) and every server answer (`dir: S`), with passwords, OAuth tokens and AUTH " +
			"payloads masked. Lines are only recorded while the account's `protocolLogs.enabled` switch is on, and " +
			"they expire with the log retention. Filters: the time range `from` / `to` (both or neither; default the " +
			"last 7 days, at most 31 days), `proto` and `connId`. Cursor paging with `limit` (default 50, max 200). " +
			"Lines live on the node that held the connection: `partial` lists nodes that did not answer in time. " +
			"Requires the `read` permission (API keys `accounts:read` or `accounts:read:<accountId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Both from and to are required when either is set."},
			httpapi.ProblemDoc{Code: errs.CodeLogRangeTooLarge, Detail: "The time range exceeds 31 days."},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "No node answered the log query; retry later."},
			httpapi.ProblemDoc{Code: errs.CodeTimeout, Detail: "The request deadline passed before the log query started."}),
		Responses:  map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: exampleProtocolLogList()}}}},
		Extensions: httpapi.WithScope(ext, "accounts:read"),
	}, a.list)
	httpapi.Register(srv, huma.Operation{
		OperationID: "downloadProtocolLogs", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/protocol-logs/download",
		Summary: "Download protocol logs", Tags: []string{"accounts"},
		Description: "Streams the account's protocol transcript for the time range as NDJSON (one `ProtocolLogRecord` " +
			"JSON object per line, oldest first), for support tickets and offline analysis. Same filters and masking " +
			"as `GET /v1/accounts/{accountId}/protocol-logs` but without paging. Writes an " +
			"`account.protocolLogs.download` audit record. A failure after the stream started ends the file early " +
			"(logged server-side). Requires the `read` permission (API keys `accounts:read` or " +
			"`accounts:read:<accountId>`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Both from and to are required when either is set."}),
		Extensions: httpapi.WithScope(ext, "accounts:read"),
		Responses: map[string]*huma.Response{"200": {Description: "NDJSON",
			Content: map[string]*huma.MediaType{"application/x-ndjson": {Schema: &huma.Schema{Type: huma.TypeString},
				Example: exampleProtocolLogNDJSON}}}},
	}, a.download)
}

type listIn struct {
	AccountID string    `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Limit     int       `query:"limit" doc:"Lines per page (default 50, 1-200)." example:"50"`
	Cursor    string    `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJ0IjoxNzkyODMzMjYyMzE4MDAwMDAwLCJpZCI6ImxvZ18wMW01ZGNrcDMwbWRleGhqNHFwMTQwZHltNCJ9"`
	From      time.Time `query:"from" doc:"Start of the time range (RFC 3339). Set both from and to, or neither for the last 7 days; at most 31 days." example:"2026-10-27T09:00:00Z"`
	To        time.Time `query:"to" doc:"End of the time range (RFC 3339)." example:"2026-10-27T10:00:00Z"`
	Proto     string    `query:"proto" enum:"imap,smtp" doc:"Only this protocol." example:"imap"`
	ConnID    string    `query:"connId" doc:"Only this connection." example:"acc_01m5cvnpb031g57sqct6mzhq94/main"`
}

type listOut struct {
	Body ProtocolLogRecordList
}

type downloadIn struct {
	AccountID string    `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	From      time.Time `query:"from" doc:"Start of the time range (RFC 3339). Set both from and to, or neither for the last 7 days; at most 31 days." example:"2026-10-27T09:00:00Z"`
	To        time.Time `query:"to" doc:"End of the time range (RFC 3339)." example:"2026-10-27T10:00:00Z"`
	Proto     string    `query:"proto" enum:"imap,smtp" doc:"Only this protocol." example:"imap"`
	ConnID    string    `query:"connId" doc:"Only this connection." example:"acc_01m5cvnpb031g57sqct6mzhq94/main"`
}

func (a *API) resolveAccount(ctx context.Context, accountID string) (orgID string, err error) {
	return a.Accounts.ProtocolLogAccount(ctx, accountID)
}

func resolveRange(from, to time.Time) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	if from.IsZero() && to.IsZero() {
		return now.Add(-defaultSpan), now.Add(time.Second), nil
	}
	if from.IsZero() || to.IsZero() {
		return time.Time{}, time.Time{}, errs.New(errs.CodeValidationFailed, "Both from and to are required when either is set.")
	}
	return from.UTC(), to.UTC(), nil
}

func (a *API) list(ctx context.Context, in *listIn) (*listOut, error) {
	orgID, err := a.resolveAccount(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	from, to, err := resolveRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit {
		return nil, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 200.")
	}
	page, err := a.collect(ctx, in.AccountID, orgID, from, to, in.Proto, in.ConnID, in.Cursor, limit)
	if err != nil {
		return nil, err
	}
	out := &listOut{Body: ProtocolLogRecordList{Data: page.recs, Partial: page.partial}}
	if out.Body.Data == nil {
		out.Body.Data = []ProtocolLogRecord{}
	}
	if page.next != "" {
		out.Body.NextCursor = &page.next
	}
	return out, nil
}

func (a *API) download(ctx context.Context, in *downloadIn) (*huma.StreamResponse, error) {
	orgID, err := a.resolveAccount(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	from, to, err := resolveRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	if a.Audit != nil {
		_ = a.Audit.ProtocolLogDownload(ctx, in.AccountID, from, to)
	}
	q := query.Query{
		From: from, To: to, Categories: []logging.Category{logging.CatProtocol},
		OrganizationID: orgID, AccountID: in.AccountID, Limit: query.MaxLimit,
	}
	proto, connID := in.Proto, in.ConnID
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		rctx := hctx.Context()
		hctx.SetHeader("Content-Type", "application/x-ndjson")
		w := hctx.BodyWriter()
		enc := json.NewEncoder(w)
		err := a.Logs.Each(rctx, q, func(p *query.Page) error {
			for i := range p.Records {
				r := p.Records[i]
				if !matchProtoConn(r, proto, connID) {
					continue
				}
				rec, ok := fromLogRecord(r)
				if !ok {
					continue
				}
				if err := enc.Encode(rec); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil && rctx.Err() == nil {
			a.log().LogAttrs(rctx, slog.LevelWarn, "protocol log download incomplete", slog.Any("err", err))
		}
	}}, nil
}

type collected struct {
	recs    []ProtocolLogRecord
	next    string
	partial []string
}

func (a *API) collect(ctx context.Context, accountID, orgID string, from, to time.Time, proto, connID, cursor string, limit int) (*collected, error) {
	out := &collected{recs: make([]ProtocolLogRecord, 0, limit)}
	q := query.Query{
		From: from, To: to, Categories: []logging.Category{logging.CatProtocol},
		OrganizationID: orgID, AccountID: accountID, Limit: query.MaxLimit, Cursor: cursor,
	}
	for {
		page, err := a.Logs.Search(ctx, q)
		if err != nil {
			return nil, err
		}
		out.partial = appendUnique(out.partial, page.Partial...)
		for i := range page.Records {
			r := page.Records[i]
			if !matchProtoConn(r, proto, connID) {
				continue
			}
			rec, ok := fromLogRecord(r)
			if !ok {
				continue
			}
			out.recs = append(out.recs, rec)
			if len(out.recs) == limit {
				out.next = query.EncodeCursor(r.Pos())
				return out, nil
			}
		}
		if page.NextCursor == "" || len(page.Records) == 0 {
			return out, nil
		}
		q.Cursor = page.NextCursor
	}
}

func appendUnique(dst []string, add ...string) []string {
	seen := map[string]bool{}
	for _, s := range dst {
		seen[s] = true
	}
	for _, s := range add {
		if !seen[s] {
			dst = append(dst, s)
			seen[s] = true
		}
	}
	return dst
}
