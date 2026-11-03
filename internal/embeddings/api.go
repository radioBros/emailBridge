package embeddings

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
	exMessageID = "msg_01m5ck6pf04wm1cyx6q10s4x5w"
	exMailboxID = "mbx_01m5cez6h04ejt72gcx84wfqjf"
	exJobID     = "job_01m5cnaee0w96fc46z350nc6q0"
	exUserID    = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

// availability is the switch paragraph shared by the descriptions.
const availability = "Needs the switch `ai.embeddings` (organisation master; an account can opt out), full-text search " +
	"(`search.fullText`) and `ai.enabled` on for the account (`403 feature_disabled` otherwise) and an embeddings " +
	"endpoint in the AI settings (`422 validation_failed` otherwise)."

func (s *Service) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P10")
	read := httpapi.WithScope(ext, "messages:read")
	common := []httpapi.ProblemDoc{
		{Code: errs.CodeUnauthenticated},
		{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
		{Code: errs.CodeScopeMissing},
		{Code: errs.CodeNotFound, Detail: "The account does not exist."},
		{Code: errs.CodeFeatureDisabled, Detail: "Semantic search is switched off for this account."},
	}
	httpapi.Register(srv, huma.Operation{
		OperationID: "searchAccountMessagesSemantic", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/semantic-search",
		Summary: "Search an account's mail by meaning", Tags: []string{"messages"},
		Description: "Finds the account's messages closest in meaning to `q` (any language; words need not match): " +
			"the query is embedded with the organisation's embeddings endpoint and compared (cosine similarity) with " +
			"the stored vectors of the messages' subject and body text. Returns the best `limit` messages (default 10, " +
			"at most 50), one per message, each with the best matching passage as `snippet` and its `score`. Filter by " +
			"message date with `from` / `to` (RFC 3339, inclusive). At most 50 000 stored chunks are compared per query " +
			"(`422 validation_failed`: narrow by date). Per account only: there is no organisation-wide semantic " +
			"search. " + availability + " The query (with e-mail addresses hashed when `piiRedact` is on) is sent to " +
			"the embeddings provider. Limited per user (`429 rate_limited`). Requires the `read` permission (API keys " +
			"`messages:read` or `messages:read:<accountId>`).",
		Metadata: httpapi.ProblemDocs(append(common,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "q is required."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The embeddings provider returned an error."})...),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{
			"data": []any{map[string]any{
				"messageId": exMessageID, "accountId": exAccountID, "mailboxId": exMailboxID, "path": "INBOX",
				"subject": "Convocazione assemblea condominiale", "from": "Amministrazione <studio@example.com>",
				"date":    "2026-10-27T09:55:12Z",
				"snippet": "…the ordinary assembly is called for 14 November at 18:00 in the meeting room…", "score": 0.8312,
			}},
			"model": "text-embedding-3-small", "chunks": 5120,
		})}},
		Extensions: read,
	}, s.searchHandler)
	httpapi.Register(srv, huma.Operation{
		OperationID: "askAccountMailbox", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/ask",
		Summary: "Ask a question about an account's mail", Tags: []string{"messages"},
		Description: "Answers `question` from the account's mail: the passages closest in meaning (as in semantic " +
			"search; `limit` excerpts, default 6, at most 12, at most 2 per message, about 12 000 characters; " +
			"optionally only messages dated between `from` and `to`, also the way past the 50 000-chunk cap of a " +
			"large mailbox) are given " +
			"to the organisation's main AI provider with a strict prompt — answer only from the excerpts, say when they " +
			"do not contain the answer, cite the messages as `[msg_…]`. Returns the `answer` (plain text) and the " +
			"`citations` (only messages that were among the excerpts). When the account has nothing embedded, `answer` " +
			"is empty and no provider is called. Bounded to 50 s. " + availability + " The question and the " +
			"excerpts (e-mail addresses hashed when `piiRedact` is on) are sent to the providers; nothing is stored " +
			"except an audit record `ai.ask` with the question (never the answer). Limited per user (`429 " +
			"rate_limited`). Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`).",
		Metadata: httpapi.ProblemDocs(append(common,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "question is required."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider returned an error."})...),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{"question": "When is the next assembly?", "limit": 6})},
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{
			"answer": "The next assembly is on 14 November at 18:00 in the meeting room [" + exMessageID + "].",
			"citations": []any{map[string]any{
				"messageId": exMessageID, "accountId": exAccountID, "mailboxId": exMailboxID,
				"subject": "Convocazione assemblea condominiale", "from": "Amministrazione <studio@example.com>",
				"date": "2026-10-27T09:55:12Z", "score": 0.8312,
			}},
			"excerpts": 6, "model": "text-embedding-3-small",
		})}},
		Extensions: read,
	}, s.askHandler)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountEmbeddings", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/embeddings",
		Summary: "Semantic search status of an account", Tags: []string{"accounts"},
		Description: "Whether semantic search and ask are available for the account (`enabled`, else `reason`: " +
			"`switch_off`, `fulltext_off`, `ai_off`, `not_configured`), the embedding model, and how much of the account " +
			"is embedded (`messages`, `chunks`) out of the messages in the full-text store (`indexedMessages`). " +
			"Read-only. Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`).",
		Metadata: httpapi.ProblemDocs(common[:4]...),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{
			"accountId": exAccountID, "enabled": true, "model": "text-embedding-3-small", "messages": 1830, "chunks": 5120,
			"indexedMessages": 1912, "maxChunks": DefaultMaxChunks,
		})}},
		Extensions: read,
	}, s.statusHandler)
	httpapi.Register(srv, huma.Operation{
		OperationID: "reindexAccountEmbeddings", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/embeddings/reindex",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Embed the account's indexed mail", Tags: []string{"accounts"},
		Description: "Queues an `embeddings.reindex` job that walks the account's full-text store, newest message " +
			"first, up to `limit` messages (default 5000, at most 50 000), and embeds those without current vectors " +
			"(mail indexed before the switch was on, after a model change, or dropped while the queue was full), " +
			"several messages per provider request within the organisation's rate limit. While one is queued or " +
			"running, that job is returned instead. " + availability + " Answers `202` with the job and `Location: " +
			"/v1/jobs/{jobId}`; the result counts scanned, embedded, already embedded, skipped and failed messages and " +
			"the chunks written. Requires `accounts.operate` (owner, admin, operator; API keys `accounts:write`). " +
			"Writes an `account.embeddings.reindex` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeAccountDeleting},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "Semantic search is switched off for this account."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "limit must be between 1 and 50000."}),
		Responses: map[string]*huma.Response{"202": {Content: jsonContent(map[string]any{"job": map[string]any{
			"id": exJobID, "organizationId": exOrgID, "kind": JobKind, "state": "queued",
			"createdBy": exUserID, "createdAt": "2026-10-30T09:30:00Z",
		}})}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.reindexHandler)
}

type accountPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

type searchIn struct {
	AccountID string    `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Q         string    `query:"q" maxLength:"500" doc:"Required. What to look for, in any language (at most 500 characters)." example:"when is the next condominium assembly"`
	Limit     int       `query:"limit" minimum:"1" maximum:"50" default:"10" doc:"Messages to return (default 10, at most 50)." example:"10"`
	From      time.Time `query:"from" doc:"Only messages dated at or after this time (RFC 3339)." example:"2026-10-01T00:00:00Z"`
	To        time.Time `query:"to" doc:"Only messages dated at or before this time (RFC 3339)." example:"2026-10-31T23:59:59Z"`
}

type searchOut struct {
	Body SemanticSearchResult
}

func (s *Service) searchHandler(ctx context.Context, in *searchIn) (*searchOut, error) {
	res, err := s.Search(ctx, SearchInput{AccountID: in.AccountID, Q: in.Q, Limit: in.Limit, From: in.From, To: in.To})
	if err != nil {
		return nil, err
	}
	return &searchOut{Body: *res}, nil
}

type askIn struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Body      struct {
		Question string    `json:"question" maxLength:"1000" doc:"The question, in any language (at most 1000 characters)." example:"When is the next assembly?"`
		Limit    int       `json:"limit,omitempty" minimum:"1" maximum:"12" doc:"Excerpts given to the model (default 6, at most 12)." example:"6"`
		From     time.Time `json:"from,omitempty" doc:"Only messages dated at or after this time (RFC 3339)." example:"2026-10-01T00:00:00Z"`
		To       time.Time `json:"to,omitempty" doc:"Only messages dated at or before this time (RFC 3339)." example:"2026-10-31T23:59:59Z"`
	}
}

type askOut struct {
	Body AskResult
}

func (s *Service) askHandler(ctx context.Context, in *askIn) (*askOut, error) {
	res, err := s.Ask(ctx, AskInput{AccountID: in.AccountID, Question: in.Body.Question, Limit: in.Body.Limit, From: in.Body.From, To: in.Body.To})
	if err != nil {
		return nil, err
	}
	return &askOut{Body: *res}, nil
}

type statusOut struct {
	Body Status
}

func (s *Service) statusHandler(ctx context.Context, in *accountPath) (*statusOut, error) {
	st, err := s.Status(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &statusOut{Body: *st}, nil
}

type reindexIn struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Limit     int    `query:"limit" minimum:"1" maximum:"50000" doc:"How many of the newest indexed messages to walk (default 5000)." example:"5000"`
}

// JobAccepted is the 202 body of the reindex operation.
type SemanticJobAccepted struct {
	Job *jobs.Job `json:"job" doc:"The queued (or already running) reindex job; poll GET /v1/jobs/{jobId} for progress and the result."`
}

type reindexOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     SemanticJobAccepted
}

func (s *Service) reindexHandler(ctx context.Context, in *reindexIn) (*reindexOut, error) {
	j, err := s.StartReindex(ctx, in.AccountID, in.Limit)
	if err != nil {
		return nil, err
	}
	return &reindexOut{Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID, Body: SemanticJobAccepted{Job: j}}, nil
}
