package fulltext

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
)

// Example identifiers (pattern-valid, the same organisation and account as the other OpenAPI examples).
const (
	exOrgID     = "org_01m5caqpk0kast63yc13wpy2d9"
	exAccountID = "acc_01m5cvnpb031g57sqct6mzhq94"
	exJobID     = "job_01m5cnaee0w96fc46z350nc6q0"
	exUserID    = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleSearch() map[string]any {
	return map[string]any{
		"data": []any{map[string]any{
			"messageId": "msg_01m5ck6pf04wm1cyx6q10s4x5w", "accountId": exAccountID, "mailboxId": "mbx_01m5cez6h04ejt72gcx84wfqjf",
			"path": "INBOX", "subject": "Invoice 2026-1043", "from": "Billing <billing@example.com>",
			"to": []any{"Reception <reception@example.com>"}, "date": "2026-10-27T09:55:12Z", "size": 48213,
			"snippet":    "…please find the invoice 2026-1043 attached. Payment is due within 30 days of the invoice date…",
			"highlights": []any{map[string]any{"start": 17, "end": 24}, map[string]any{"start": 82, "end": 89}},
			"score":      1.75,
		}},
		"total": 1, "totalCapped": false,
	}
}

func (s *Service) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P10")
	httpapi.Register(srv, huma.Operation{
		OperationID: "searchMessagesFullText", Method: http.MethodGet, Path: "/v1/messages/search",
		Summary: "Full-text search across accounts", Tags: []string{"messages"},
		Description: "Searches the text of the organisation's messages in every account (subject, sender and body " +
			"text, the first 64 KiB of each body) in EmailBridge's own text index, instead of the provider's per-account " +
			"search. Needs the switch `search.fullText` for the organisation (`403 feature_disabled` while it is off); " +
			"accounts can opt out. Messages are indexed as they arrive; older mail after `POST /v1/accounts/{accountId}/" +
			"fulltext/reindex`. `q`: words (any of them matches), `\"exact phrases\"` and `-excluded` words; matching is " +
			"case- and accent-insensitive on whole words, without stemming. Results are ordered by relevance, then " +
			"newest first, each with a plain-text `snippet` around the first match and the match positions in it " +
			"(`highlights`, code points). Filter by `accountId` and by the message date (`from` / `to`, RFC 3339, " +
			"inclusive). Paged with `page` and `limit` (default 25, at most 100) over the first 1000 matches; `total` " +
			"is capped at 1000 (`totalCapped`). Requires the `read` permission (API keys `messages:read`; a key with " +
			"only per-account scopes must name `accountId`; an account-bound key always searches its own account). " +
			"Read-only; no connection to the mail servers is made.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "Full-text search is switched off for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "q is required."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleSearch())}},
		Extensions: httpapi.WithScope(ext, "messages:read"),
	}, s.searchHandler)
	httpapi.Register(srv, huma.Operation{
		OperationID: "reindexAccountFullText", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/fulltext/reindex",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Rebuild the account's full-text index", Tags: []string{"accounts"},
		Description: "Queues a `fulltext.reindex` job that walks the account's message index, most recently indexed " +
			"first, up to `limit` messages (default 5000, at most 50 000), and adds the messages that are not in the " +
			"full-text index yet (for example mail that arrived before the switch was turned on, or a folder whose " +
			"UIDVALIDITY changed). Bodies are fetched from the mail server one at a time with a pause in between, so " +
			"the account stays usable. While a reindex of the account is queued or running, that job is returned " +
			"instead of a new one. Needs `search.fullText` on for the account (`403 feature_disabled`). Answers `202` " +
			"with the job and `Location: /v1/jobs/{jobId}`; the result counts scanned, indexed, already indexed, " +
			"skipped and failed messages. Requires `accounts.operate` (owner, admin, operator; API keys " +
			"`accounts:write`). Writes an `account.fulltext.reindex` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeAccountDeleting},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "Full-text search is switched off for this account."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "limit must be between 1 and 50000."}),
		Responses: map[string]*huma.Response{"202": {Content: jsonContent(map[string]any{"job": map[string]any{
			"id": exJobID, "organizationId": exOrgID, "kind": JobKind, "state": "queued",
			"createdBy": exUserID, "createdAt": "2026-10-30T09:30:00Z",
		}})}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.reindexHandler)
}

type searchIn struct {
	Q         string    `query:"q" maxLength:"256" doc:"Required. Search text: words, \"exact phrases\", -excluded words (at most 256 characters)." example:"invoice \"payment due\" -reminder"`
	AccountID string    `query:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Only this account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	From      time.Time `query:"from" doc:"Only messages dated at or after this time (RFC 3339)." example:"2026-10-01T00:00:00Z"`
	To        time.Time `query:"to" doc:"Only messages dated at or before this time (RFC 3339)." example:"2026-10-31T23:59:59Z"`
	Page      int       `query:"page" minimum:"1" default:"1" doc:"1-based page number." example:"1"`
	Limit     int       `query:"limit" minimum:"1" maximum:"100" default:"25" doc:"Page size (default 25, at most 100)." example:"25"`
}

type searchOut struct {
	Body FullTextSearchResult
}

func (s *Service) searchHandler(ctx context.Context, in *searchIn) (*searchOut, error) {
	res, err := s.Search(ctx, SearchInput{Q: in.Q, AccountID: in.AccountID, From: in.From, To: in.To, Page: in.Page, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &searchOut{Body: *res}, nil
}

type reindexIn struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Limit     int    `query:"limit" minimum:"1" maximum:"50000" doc:"How many of the most recently indexed messages to walk (default 5000)." example:"5000"`
}

// JobAccepted is the 202 body of the reindex operation.
type FullTextJobAccepted struct {
	Job *jobs.Job `json:"job" doc:"The queued (or already running) reindex job; poll GET /v1/jobs/{jobId} for progress and the result."`
}

type reindexOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     FullTextJobAccepted
}

func (s *Service) reindexHandler(ctx context.Context, in *reindexIn) (*reindexOut, error) {
	j, err := s.StartReindex(ctx, in.AccountID, in.Limit)
	if err != nil {
		return nil, err
	}
	return &reindexOut{Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID, Body: FullTextJobAccepted{Job: j}}, nil
}
