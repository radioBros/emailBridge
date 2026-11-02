package apikeys

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listApiKeys", Method: http.MethodGet, Path: "/v1/api-keys",
		Summary: "List API keys", Tags: []string{"api-keys"},
		Description: "Lists the API keys of the active organisation, revoked ones included (`revokedAt` set). Secrets are " +
			"never returned: a key is recognised by `prefix` + `last4`. Requires the `read` permission (every role; API " +
			"keys `apikeys:read`). `q` matches part of the name or prefix. Pagination: `limit` (default 50, max 200) with " +
			"`cursor` from `nextCursor`, or `page` with `includeTotal=true`. Sort `name`, `createdAt`, `lastUsedAt` or " +
			"`id` (prefix `-` for descending; default `-id`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleKeyList())}},
		Extensions: httpapi.WithScope(ext, "api-keys:read"),
	}, m.listKeys)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createApiKey", Method: http.MethodPost, Path: "/v1/api-keys",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create API key", Tags: []string{"api-keys"},
		Description: "Creates an API key for machine clients in the active organisation and returns its secret `token` " +
			"once — store it, it cannot be shown again (only a hash is kept). Clients send it as " +
			"`Authorization: Bearer ebk_…`. `scopes` limit what the key may do (`accounts:read`, `outbound:write`, " +
			"`accounts:read:<acc_…>` for one account, `admin` for everything …); `accountId` binds the key to one " +
			"account; `restrictions` limit client IPs and referrers; `expiresAt` sets an expiry. An API key creating a " +
			"key can only grant scopes it holds. Requires `apikeys.write` (owner, admin; API keys `apikeys:write`). " +
			"Writes an `apikey.create` audit record. Supports `Idempotency-Key` (a replay shows `token` as " +
			"`[redacted]`: the secret is never stored); returns `Location` and `ETag`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing, Detail: "The API key lacks a scope needed to grant it."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Unknown or malformed scope."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "accountId does not name an account of this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "apikeys.accountBound is disabled."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"integration": {
				Summary: "Organisation-wide integration key",
				Value: map[string]any{
					"name": "CRM integration", "scopes": []any{"accounts:read", "messages:read", "outbound:write"},
					"restrictions": map[string]any{"ips": []any{"203.0.113.0/24"}},
					"expiresAt":    "2027-10-27T00:00:00Z",
				},
			},
			"accountBound": {
				Summary:     "Key bound to one account",
				Description: "A device or app that may only read and send for the reception mailbox.",
				Value: map[string]any{
					"name": "Reception kiosk", "accountId": "acc_01m5cvnpb031g57sqct6mzhq94",
					"scopes": []any{"messages:read", "outbound:write"},
				},
			},
		})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonContent(exampleCreatedKey())}},
		Extensions: httpapi.WithScope(ext, "api-keys:write"),
	}, m.createKey)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getApiKey", Method: http.MethodGet, Path: "/v1/api-keys/{keyId}",
		Summary: "Get API key", Tags: []string{"api-keys"},
		Description: "Returns one API key (without its secret) with its `ETag`. Requires the `read` permission (API keys " +
			"`apikeys:read`). A key of another organisation is `404 not_found`. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleKey())}},
		Extensions: httpapi.WithScope(ext, "api-keys:read"),
	}, m.getKey)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchApiKey", Method: http.MethodPatch, Path: "/v1/api-keys/{keyId}",
		Summary: "Rename or rescope API key", Tags: []string{"api-keys"},
		Description: "Changes the name, scopes, account binding, restrictions, rate limit or expiry of a key (JSON Merge " +
			"Patch: omitted fields stay, `accountId: null` / `expiresAt: null` remove them). The secret does not change; " +
			"cached verifications are dropped so new scopes apply at once. A revoked key cannot be changed " +
			"(`409 conflict`). Requires `apikeys.write` (API keys `apikeys:write`, and only scopes they hold). " +
			"Send the last `ETag` as `If-Match` (`412 version_mismatch`). Writes an `apikey.update` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeConflict, errs.CodeFeatureDisabled,
			errs.CodeVersionMismatch, errs.CodeRateLimited),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"rescope": {Summary: "Reduce the scopes", Value: map[string]any{"scopes": []any{"accounts:read", "messages:read"}}},
			"unbind":  {Summary: "Remove account binding and expiry", Value: map[string]any{"accountId": nil, "expiresAt": nil}},
		})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleKey())}},
		Extensions: httpapi.WithScope(ext, "api-keys:write"),
	}, m.patchKey)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteApiKey", Method: http.MethodDelete, Path: "/v1/api-keys/{keyId}",
		Summary: "Revoke API key", Tags: []string{"api-keys"},
		Description: "Revokes the key immediately (its cached verifications are purged, so the next request with it fails " +
			"with `401 api_key_revoked`) and answers `204`. The record stays listed with `revokedAt` for the audit trail. " +
			"Idempotent: revoking a revoked key also answers `204`. Requires `apikeys.write` (API keys `apikeys:write`); " +
			"`If-Match` is honoured. Writes an `apikey.revoke` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeVersionMismatch),
		Extensions: httpapi.WithScope(ext, "api-keys:write"),
	}, m.deleteKey)
}

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleKey() map[string]any {
	return map[string]any{
		"id": "key_01m5cqe6d0rs9cyxvy69ynf0t6", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "CRM integration", "prefix": "ebk_", "last4": "x3Qa",
		"scopes":       []any{"accounts:read", "messages:read", "outbound:write"},
		"restrictions": map[string]any{"ips": []any{"203.0.113.0/24"}},
		"expiresAt":    "2027-10-27T00:00:00Z", "lastUsedAt": "2026-10-27T09:41:18Z",
		"createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-21T14:05:00Z",
		"updatedAt": "2026-10-24T08:12:30Z", "version": 2,
	}
}

func exampleCreatedKey() map[string]any {
	k := exampleKey()
	k["version"] = 1
	k["updatedAt"] = k["createdAt"]
	delete(k, "lastUsedAt")
	k["token"] = "ebk_Zt4mQ8vK2xR7pW1nB6cH9dL3fJ5gY0sA_x3Qa"
	return k
}

func exampleKeyList() map[string]any {
	return map[string]any{
		"data": []any{exampleKey(), map[string]any{
			"id": "key_01m5k62yn1ex6zgtrm72s576kf", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
			"name": "Reception kiosk", "prefix": "ebk_", "last4": "M7pd", "accountId": "acc_01m5cvnpb031g57sqct6mzhq94",
			"scopes": []any{"messages:read", "outbound:write"}, "revokedAt": "2026-10-26T18:00:00Z",
			"createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-19T10:00:00Z",
			"updatedAt": "2026-10-26T18:00:00Z", "version": 3,
		}},
		"nextCursor": "eyJpZCI6ImtleV8wMW01azYyeW4xZXg2emd0cm03MnM1NzZrZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImtleV8wMW01azYyeW4xZXg2emd0cm03MnM1NzZrZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"name, createdAt, lastUsedAt or id; prefix - for descending. Default -id." example:"-lastUsedAt"`
	Q            string `query:"q" doc:"Case-insensitive part of the name or prefix (≤ 200 characters)." example:"crm"`
}

type listOut struct {
	Body KeyList
}

type createBody struct {
	Name         string        `json:"name" doc:"Display name (required, not blank)." example:"CRM integration"`
	Scopes       []string      `json:"scopes" doc:"Scopes to grant (at least one, unique). An API key creating a key can only grant scopes it holds itself." example:"[\"accounts:read\",\"messages:read\",\"outbound:write\"]"`
	AccountID    string        `json:"accountId,omitempty" doc:"Bind the key to one account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Restrictions *Restrictions `json:"restrictions,omitempty" doc:"Optional client IP / referrer restrictions."`
	RateLimit    *RateLimit    `json:"rateLimit,omitempty" doc:"Optional per-key rate limit hint."`
	ExpiresAt    *time.Time    `json:"expiresAt,omitempty" doc:"Optional expiry (RFC 3339, in the future)." example:"2027-10-27T00:00:00Z"`
}

type createIn struct {
	Body createBody
}

type createdOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new key." example:"/v1/api-keys/key_01m5cqe6d0rs9cyxvy69ynf0t6"`
	ETag     string `header:"ETag" doc:"Version of the new key." example:"\"v1\""`
	Body     createdBody
}

type createdBody struct {
	Key
	Token string `json:"token" doc:"The secret, shown only in this response; send it as Authorization: Bearer <token>. EmailBridge stores only a hash." example:"ebk_Zt4mQ8vK2xR7pW1nB6cH9dL3fJ5gY0sA_x3Qa"`
}

type keyIDIn struct {
	KeyID string `path:"keyId" doc:"API key ID (key_…)." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
}

type getOut struct {
	ETag string `header:"ETag" doc:"Current version; send as If-Match on PATCH / DELETE." example:"\"v2\""`
	Body Key
}

type patchBody struct {
	Name         *string         `json:"name,omitempty" doc:"New display name (not blank)." example:"CRM integration (prod)"`
	Scopes       *[]string       `json:"scopes,omitempty" doc:"Replaces the granted scopes (same rules as on create)." example:"[\"accounts:read\",\"messages:read\"]"`
	AccountID    json.RawMessage `json:"accountId,omitempty" doc:"Bind to this account; null removes the binding." example:"\"acc_01m5cvnpb031g57sqct6mzhq94\""`
	Restrictions *Restrictions   `json:"restrictions,omitempty" doc:"Replaces the restrictions."`
	RateLimit    *RateLimit      `json:"rateLimit,omitempty" doc:"Replaces the rate limit hint."`
	ExpiresAt    json.RawMessage `json:"expiresAt,omitempty" doc:"New expiry (RFC 3339 string, in the future); null removes it." example:"\"2027-04-30T00:00:00Z\""`
}

type patchIn struct {
	KeyID   string `path:"keyId" doc:"API key ID (key_…)." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
	Body    patchBody
}

type deleteIn struct {
	KeyID   string `path:"keyId" doc:"API key ID (key_…)." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
}

func (m *Module) listKeys(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) createKey(ctx context.Context, in *createIn) (*createdOut, error) {
	k, token, err := m.svc.Create(ctx, CreateInput{
		Name: in.Body.Name, Scopes: in.Body.Scopes, AccountID: in.Body.AccountID,
		Restrictions: in.Body.Restrictions, RateLimit: in.Body.RateLimit, ExpiresAt: in.Body.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	return &createdOut{
		Status:   http.StatusCreated,
		Location: "/v1/api-keys/" + k.ID,
		ETag:     formatETag(k.Version),
		Body:     createdBody{Key: k, Token: token},
	}, nil
}

func (m *Module) getKey(ctx context.Context, in *keyIDIn) (*getOut, error) {
	k, err := m.svc.Get(ctx, in.KeyID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(k.Version), Body: k}, nil
}

func (m *Module) patchKey(ctx context.Context, in *patchIn) (*getOut, error) {
	acc, err := parseNullableString(in.Body.AccountID)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, "accountId must be a string or null.")
	}
	exp, err := parseNullableTime(in.Body.ExpiresAt)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, "expiresAt must be an RFC 3339 timestamp or null.")
	}
	k, err := m.svc.Patch(ctx, in.KeyID, PatchInput{
		Name:         in.Body.Name,
		Scopes:       in.Body.Scopes,
		AccountID:    acc,
		Restrictions: in.Body.Restrictions,
		RateLimit:    in.Body.RateLimit,
		ExpiresAt:    exp,
	}, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(k.Version), Body: k}, nil
}

func parseNullableString(raw json.RawMessage) (*nullableString, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var n nullableString
	if err := n.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &n, nil
}

func parseNullableTime(raw json.RawMessage) (*nullableTime, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var n nullableTime
	if err := n.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &n, nil
}

func (m *Module) deleteKey(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.svc.Revoke(ctx, in.KeyID, in.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}
