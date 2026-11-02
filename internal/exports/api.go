package exports

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Register adds export HTTP routes.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P7")
	const readPerm = "Requires the `read` permission (every role; API keys `exports:read`, an account-bound key only for its account). "
	const writePerm = "Requires `accounts.operate` (owner, admin, operator; API keys `accounts:write`, an account-bound key only for its account). "
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the scope this operation needs."}}
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, auth...), extra...)...)
	}
	accountNF := httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."}
	exportNF := httpapi.ProblemDoc{Code: errs.CodeExportNotFound, Detail: "The export does not exist."}
	ndjson := func(desc string) *huma.MediaType {
		return &huma.MediaType{
			Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: desc},
			Example: exampleNDJSON,
		}
	}
	downloadHeaders := func() map[string]*huma.Param {
		return map[string]*huma.Param{
			"Content-Encoding": {Description: "Always gzip: the body is gzip-compressed NDJSON (HTTP clients that decompress automatically see plain NDJSON).",
				Schema: &huma.Schema{Type: huma.TypeString}, Example: "gzip"},
			"Content-Disposition": {Description: "Download as <exportId>.ndjson.gz.", Schema: &huma.Schema{Type: huma.TypeString},
				Example: `attachment; filename="exp_01m5dckp30rpt94yha22n10ktn.ndjson.gz"`},
		}
	}
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccountExports", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/exports", Summary: "List bulk exports",
		Description: "Lists the account's bulk exports with state and progress, newest first by default " +
			"(`-createdAt`; `sort` also `createdAt`, `id`, `-id`). Paged by `cursor` (`nextCursor` absent on the last " +
			"page) or `page`; `limit` 1–200 (default 50); `includeTotal=true` adds `total`. An account of another " +
			"organisation is `404 not_found`. " + readPerm + "Read-only.",
		Metadata:  problems(accountNF, httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed}, httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The cursor is invalid."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exampleExportList())}},
		Tags:      []string{"accounts"}, Extensions: httpapi.WithScope(ext, "exports:read"),
	}, s.listExports)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createAccountExport", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/exports", Summary: "Start a bulk export",
		Description: "Queues a background job (`account.export`) that writes every indexed message of the account — or " +
			"of `folderIds`, received in `[since, before)` — as one JSON object per line (NDJSON), gzip-compressed and " +
			"encrypted at rest (AES-GCM). `include` picks the content: `metadataOnly` (the default), `raw` (base64 RFC " +
			"822) and/or `attachments` (base64 parts). Answers `202` with the export, the job and `Location`; poll " +
			"`GET …/exports/{exportId}` or wait for the `export.completed` / `export.failed` event, then download. At " +
			"most 3 active exports per account (`409 conflict`). Needs the `export.enabled` switch (`403 " +
			"feature_disabled`). Finished exports are kept 7 days. " + writePerm + "Writes an `export.created` audit record.",
		Metadata: problems(accountNF,
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "export.enabled is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "metadataOnly cannot be combined with raw or attachments."},
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "This account already has the maximum number of active exports."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"metadata": {Summary: "Metadata of one folder for October", Value: map[string]any{
				"folderIds": []any{"mbx_01m5cez6h04ejt72gcx84wfqjf"}, "since": "2026-10-01T00:00:00Z",
				"before": "2026-11-01T00:00:00Z", "include": map[string]any{"metadataOnly": true},
			}},
			"full": {Summary: "Everything with raw source and attachments", Value: map[string]any{
				"include": map[string]any{"raw": true, "attachments": true},
			}},
		})},
		Responses: map[string]*huma.Response{"202": {Content: jsonExample(map[string]any{
			"export": exampleExportQueued(),
			"job": map[string]any{
				"id": "job_01m5dckp30v2y7c9k1d4r8e6wq", "organizationId": exampleOrgID, "kind": "account.export",
				"state": "queued", "createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-27T10:15:00Z",
			},
		})}},
		DefaultStatus: http.StatusAccepted, Tags: []string{"accounts"},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.createExport)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountExport", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/exports/{exportId}", Summary: "Get an export",
		Description: "Returns one export: `state` (queued, running, succeeded, failed, cancelled, expired), `progress` " +
			"(`done` / `total` messages), and once succeeded `messageCount`, `byteSize` and `expireAt` (when the file " +
			"is deleted). A failed export carries the problem in `error`. " + readPerm + "Read-only.",
		Metadata:  problems(exportNF),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exampleExportDone())}},
		Tags:      []string{"accounts"}, Extensions: httpapi.WithScope(ext, "exports:read"),
	}, s.getExport)
	httpapi.Register(srv, huma.Operation{
		OperationID: "cancelAccountExport", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/exports/{exportId}/cancel", Summary: "Cancel an export",
		Description: "Stops a queued or running export: a queued one becomes `cancelled` at once, a running one when its " +
			"job notices the cancel request (the answer may still say `running`). An export that already finished " +
			"(succeeded, failed, cancelled, expired) is `409 conflict`. " + writePerm + "Writes an `export.cancelled` audit record.",
		Metadata: problems(exportNF,
			httpapi.ProblemDoc{Code: errs.CodeConflict, Detail: "The export is already succeeded."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			e := exampleExportQueued()
			e["state"] = "cancelled"
			e["finishedAt"] = "2026-10-27T10:15:40Z"
			return e
		}())}},
		Tags: []string{"accounts"}, Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.cancelExport)
	httpapi.Register(srv, huma.Operation{
		OperationID: "downloadAccountExport", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/exports/{exportId}/download", Summary: "Download an export",
		Description: "Streams a succeeded export as gzip-compressed NDJSON (decrypted on the fly; one message per line, see " +
			"the example). With `url=signed` the answer is JSON `{ url, expiresAt }`: a public `/e/{token}` link valid " +
			"for 15 minutes by default (at most 24 hours) to hand to another system. Not finished yet is `409 " +
			"export_not_ready`; past `expireAt` it is `410 export_expired`. " + readPerm + "Read-only.",
		Metadata: problems(exportNF,
			httpapi.ProblemDoc{Code: errs.CodeExportNotReady, Detail: "The export is not ready for download."},
			httpapi.ProblemDoc{Code: errs.CodeExportExpired, Detail: "The export has expired."}),
		Tags: []string{"accounts"}, Extensions: httpapi.WithScope(ext, "exports:read"),
		Responses: map[string]*huma.Response{"200": {Description: "Gzip NDJSON or signed URL", Headers: downloadHeaders(),
			Content: map[string]*huma.MediaType{
				"application/x-ndjson": ndjson("Gzip-compressed NDJSON, one ExportLine object per line."),
				"application/json": {
					Schema:  srv.API.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[SignedExportURL](), true, ""),
					Example: map[string]any{"url": "https://emailbridge.example.com/e/" + exampleExportToken, "expiresAt": "2026-10-27T11:15:00Z"},
				},
			}}},
	}, s.downloadExport)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getSignedExport", Method: http.MethodGet,
		Path: "/e/{token}", Summary: "Public signed export download",
		Description: "Public download behind the `url` from `GET …/download?url=signed`; the HMAC token is the credential " +
			"(bound to the organisation and the export, short-lived). Streams the same gzip NDJSON as the authenticated " +
			"download. A tampered token is `403 signed_url_invalid`, an expired one `403 signed_url_expired`; an export " +
			"past retention is `410 export_expired`. No cookie or API key needed. Read-only.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeSignedURLInvalid, Detail: "The signed export URL is invalid."},
			httpapi.ProblemDoc{Code: errs.CodeSignedURLExpired, Detail: "The signed export URL has expired."},
			exportNF,
			httpapi.ProblemDoc{Code: errs.CodeExportNotReady, Detail: "The export blob is missing."},
			httpapi.ProblemDoc{Code: errs.CodeExportExpired, Detail: "The export object is gone."}),
		Tags: []string{"accounts"}, Extensions: ext, Security: []map[string][]string{},
		Responses: map[string]*huma.Response{"200": {Description: "Gzip NDJSON", Headers: downloadHeaders(),
			Content: map[string]*huma.MediaType{"application/x-ndjson": ndjson("Gzip-compressed NDJSON, one ExportLine object per line.")}}},
	}, s.signedDownload)
}

// Documentation examples (W70).
const (
	exampleOrgID       = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleAccountID   = "acc_01m5cvnpb031g57sqct6mzhq94"
	exampleExportID    = "exp_01m5dckp30rpt94yha22n10ktn"
	exampleExportToken = "djF8MTc5MzA5OTcwMHxvcmdfMDFtNWNhcXBrMGthc3Q2M3ljMTN3cHkyZDl8ZXhwXzAxbTVkY2twMzBycHQ5NHloYTIybjEwa3Ru.rOkzz36fKj3uLEbDC2vxSnL0ZWQM0FlWAfqh5Pypky8" //nolint:gosec // G101: documentation example signed URL token, not a real secret.
	exampleNDJSON      = `{"id":"msg_01m5ck6pf04wm1cyx6q10s4x5w","mailboxId":"mbx_01m5cez6h04ejt72gcx84wfqjf","mailboxPath":"INBOX","uid":4811,"uidValidity":1729500000,"flags":["\\Seen"],"messageIdHeader":"<20261027095512.4f2a@example.com>","emailId":"M8e1f4a7c92d5b3e6","threadId":"T5b0c9d2e71a4f863","internalDate":"2026-10-27T09:55:14Z","size":48213}` + "\n" +
		`{"id":"msg_01m5cshyc0q8tywzeefhqwnkz7","mailboxId":"mbx_01m5cez6h04ejt72gcx84wfqjf","mailboxPath":"INBOX","uid":4807,"uidValidity":1729500000,"messageIdHeader":"<CAF3k9x2Lq7@mail.example.net>","emailId":"M8e1f4a6b01c2d9a4","internalDate":"2026-10-27T08:41:02Z","size":9120}` + "\n"
)

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleExportQueued() map[string]any {
	return map[string]any{
		"id": exampleExportID, "organizationId": exampleOrgID, "accountId": exampleAccountID, "state": "queued",
		"jobId": "job_01m5dckp30v2y7c9k1d4r8e6wq",
		"filters": map[string]any{
			"folderIds": []any{"mbx_01m5cez6h04ejt72gcx84wfqjf"}, "since": "2026-10-01T00:00:00Z",
			"before": "2026-11-01T00:00:00Z", "include": map[string]any{"metadataOnly": true},
		},
		"progress":  map[string]any{"done": 0, "total": 0},
		"createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-27T10:15:00Z",
	}
}

func exampleExportDone() map[string]any {
	e := exampleExportQueued()
	e["state"] = "succeeded"
	e["progress"] = map[string]any{"done": 4795, "total": 4795, "partCount": 2}
	e["messageCount"] = 4795
	e["byteSize"] = 1873402
	e["startedAt"] = "2026-10-27T10:15:02Z"
	e["finishedAt"] = "2026-10-27T10:18:47Z"
	e["expireAt"] = "2026-11-03T10:18:47Z"
	return e
}

func exampleExportList() map[string]any {
	failed := map[string]any{
		"id": "exp_01m5d8c6505j11wvg1egg4eh7e", "organizationId": exampleOrgID, "accountId": exampleAccountID,
		"state": "failed", "jobId": "job_01m5d8c650h3k9q2w7e5r1t6yb",
		"filters":  map[string]any{"include": map[string]any{"raw": true, "attachments": true}},
		"progress": map[string]any{"done": 1200, "total": 4790},
		"error": map[string]any{
			"type": "https://emailbridge.dev/problems/internal_error", "title": "Internal error", "status": 500,
			"code": "internal_error", "kind": "internal", "detail": "An internal error occurred.",
		},
		"createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-26T16:02:10Z",
		"startedAt": "2026-10-26T16:02:12Z", "finishedAt": "2026-10-26T16:05:31Z",
	}
	return map[string]any{
		"data":       []any{exampleExportDone(), failed},
		"nextCursor": "eyJpZCI6ImV4cF8wMW01ZDhjNjUwNWoxMXd2ZzFlZ2c0ZWg3ZSIsImZpZWxkIjoiY3JlYXRlZEF0IiwiZGVzYyI6dHJ1ZX0",
	}
}

// AccountPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type AccountPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

type listIn struct {
	AccountPath
	Limit        int    `query:"limit" doc:"Page size 1–200 (default 50)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImV4cF8wMW01ZDhjNjUwNWoxMXd2ZzFlZ2c0ZWg3ZSIsImZpZWxkIjoiY3JlYXRlZEF0IiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total (exact count of the account's exports)." example:"true"`
	Sort         string `query:"sort" doc:"createdAt or id; prefix - for descending. Default -createdAt." example:"-createdAt"`
}

type listOut struct {
	Body List
}

func (s *Service) listExports(ctx context.Context, in *listIn) (*listOut, error) {
	l, err := s.List(ctx, in.AccountID, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal, Sort: in.Sort,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *l}, nil
}

type createIn struct {
	AccountPath
	Body CreateInput
}

type acceptedOut struct {
	Location string `header:"Location"`
	Status   int
	Body     Accepted
}

func (s *Service) createExport(ctx context.Context, in *createIn) (*acceptedOut, error) {
	a, err := s.Create(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &acceptedOut{
		Status:   http.StatusAccepted,
		Location: fmt.Sprintf("/v1/accounts/%s/exports/%s", in.AccountID, a.Export.ID),
		Body:     *a,
	}, nil
}

// ExportPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type ExportPath struct {
	AccountPath
	ExportID string `path:"exportId" pattern:"^exp_[0-9a-z]{26}$" doc:"Export ID (exp_…)." example:"exp_01m5dckp30rpt94yha22n10ktn"`
}

type exportOut struct {
	Body Export
}

func (s *Service) getExport(ctx context.Context, in *ExportPath) (*exportOut, error) {
	e, err := s.Get(ctx, in.AccountID, in.ExportID)
	if err != nil {
		return nil, err
	}
	return &exportOut{Body: *e}, nil
}

func (s *Service) cancelExport(ctx context.Context, in *ExportPath) (*exportOut, error) {
	e, err := s.Cancel(ctx, in.AccountID, in.ExportID)
	if err != nil {
		return nil, err
	}
	return &exportOut{Body: *e}, nil
}

type downloadIn struct {
	ExportPath
	URL string `query:"url" enum:"signed" doc:"signed: return a short-lived public download URL as JSON instead of the file." example:"signed"`
}

func (s *Service) downloadExport(ctx context.Context, in *downloadIn) (*huma.StreamResponse, error) {
	if err := tenancy.RequireReadAccount(ctx, "exports", in.AccountID); err != nil {
		return nil, err
	}
	if strings.EqualFold(in.URL, "signed") {
		body, err := s.SignedURL(ctx, in.AccountID, in.ExportID)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "application/json")
			w := hctx.BodyWriter()
			_, _ = w.Write(mustJSON(body))
		}}, nil
	}
	rc, e, err := s.OpenDownload(ctx, in.AccountID, in.ExportID)
	if err != nil {
		return nil, err
	}
	return streamExport(e.ID, rc), nil
}

type signedIn struct {
	Token string `path:"token" doc:"Signed token from the url field (base64url payload, a dot, base64url HMAC-SHA256)." example:"djF8MTc5MzA5OTcwMHxvcmdfMDFtNWNhcXBrMGthc3Q2M3ljMTN3cHkyZDl8ZXhwXzAxbTVkY2twMzBycHQ5NHloYTIybjEwa3Ru.rOkzz36fKj3uLEbDC2vxSnL0ZWQM0FlWAfqh5Pypky8"`
}

func (s *Service) signedDownload(ctx context.Context, in *signedIn) (*huma.StreamResponse, error) {
	rc, e, err := s.DownloadSigned(ctx, in.Token)
	if err != nil {
		return nil, err
	}
	return streamExport(e.ID, rc), nil
}

func streamExport(id string, rc io.ReadCloser) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		writeExportStream(hctx, id, rc)
	}}
}

type headerWriter interface {
	SetHeader(string, string)
	BodyWriter() io.Writer
}

func writeExportStream(h headerWriter, id string, rc io.ReadCloser) {
	defer func() { _ = rc.Close() }()
	h.SetHeader("Content-Type", "application/x-ndjson")
	h.SetHeader("Content-Encoding", "gzip")
	h.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.ndjson.gz"`, id))
	_, _ = io.Copy(h.BodyWriter(), rc)
}
