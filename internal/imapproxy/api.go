package imapproxy

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// Register adds IMAP proxy credential HTTP routes.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P7")
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountIMAPProxyPassword", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/imap-proxy-password", Summary: "IMAP proxy password status",
		Description: "Says whether the account has a password for EmailBridge's IMAP proxy (switch `protocol.imapProxy`, listener `EB_IMAP_PROXY_ADDR`) and when it was last set; the password itself is never returned (only a salted hash is stored). Mail clients log in to the proxy with the account's email address or ID and this password and see the real mailbox, without knowing the mailbox password or OAuth token. Requires the `read` permission (API keys `accounts:read`). Read-only.",
		Metadata:    httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound),
		Responses:   map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{"set": true, "updatedAt": "2026-10-25T16:20:00Z"}}}}},
		Tags:        []string{"accounts"}, Extensions: httpapi.WithScope(ext, "accounts:read"),
	}, s.getPassword)
	httpapi.Register(srv, huma.Operation{
		OperationID: "putAccountIMAPProxyPassword", Method: http.MethodPut,
		Path: "/v1/accounts/{accountId}/imap-proxy-password", Summary: "Set IMAP proxy password",
		Description: "Sets or replaces the account's IMAP proxy password (at least 12 characters, leading and trailing spaces trimmed). Clients then log in to the proxy with the account's email address (or `acc_…` ID) and this password; EmailBridge opens the upstream session with the account's own credentials. Requires `accounts.write` (owner, admin, operator; API keys `accounts:write`). Returns the new status; the password is never echoed.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeNotFound},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Password must be at least 12 characters."}),
		Tags: []string{"accounts"}, Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.putPassword)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteAccountIMAPProxyPassword", Method: http.MethodDelete,
		Path: "/v1/accounts/{accountId}/imap-proxy-password", Summary: "Clear IMAP proxy password",
		Description: "Removes the account's IMAP proxy password; new proxy logins for the account fail from then on. Idempotent: deleting a password that is not set also answers `204`. Requires `accounts.write` (API keys `accounts:write`).",
		Metadata:    httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound),
		Tags:        []string{"accounts"}, Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, s.deletePassword)
	documentBodyFields(srv, http.MethodPut, "/v1/accounts/{accountId}/imap-proxy-password", map[string]bodyFieldDoc{
		"password": {doc: "New password, at least 12 characters (leading and trailing spaces are trimmed; stored only as a salted hash).", example: "Kq7!vR2m-Tz9wLp4"},
	})
}

// AccountPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type AccountPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

type statusOut struct {
	Body PasswordStatus
}

func (s *Service) getPassword(ctx context.Context, in *AccountPath) (*statusOut, error) {
	st, err := s.Status(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &statusOut{Body: *st}, nil
}

type putIn struct {
	AccountPath
	Body struct {
		Password string `json:"password"`
	}
}

func (s *Service) putPassword(ctx context.Context, in *putIn) (*statusOut, error) {
	st, err := s.SetPassword(ctx, in.AccountID, in.Body.Password)
	if err != nil {
		return nil, err
	}
	return &statusOut{Body: *st}, nil
}

type emptyOut struct{}

func (s *Service) deletePassword(ctx context.Context, in *AccountPath) (*emptyOut, error) {
	if err := s.ClearPassword(ctx, in.AccountID); err != nil {
		return nil, err
	}
	return &emptyOut{}, nil
}
