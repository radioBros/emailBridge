package health

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P6")
	httpapi.Register(server, huma.Operation{
		OperationID: "getAccountHealth", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/health", Summary: "Diagnose account health",
		Description: "Explains what is wrong with a mail account and how to fix it, from stored facts only (no network " +
			"call): state and its reason, recent errors, protocol facts (TLS, IDLE, CONDSTORE, quota), and hints " +
			"with concrete fixes (app password required, IMAP disabled, wrong host/port/TLS, revoked OAuth " +
			"consent …). Any member with read access to the account (API keys `accounts:read`, also granular). " +
			"Read-only.",
		Metadata:  httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: accountHealthExample(false)}}}},
		Tags:      []string{"health", "accounts"}, Extensions: httpapi.WithScope(phase, "accounts:read"),
	}, m.getHealth)
	httpapi.Register(server, huma.Operation{
		OperationID: "checkAccountHealth", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/health/check", Summary: "Run a live account health check",
		Description: "Connects to the account's IMAP server now (connect, TLS, login, capabilities) and returns the " +
			"diagnosis with a `liveCheck` block. Requires `accounts.operate` on the account (API keys " +
			"`accounts:write`). Limited to 10 checks per minute per organisation (`429`). The account's sync " +
			"and stored state are not changed. No request body.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound,
			errs.CodeRateLimited),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: accountHealthExample(true)}}}},
		Tags:      []string{"health", "accounts"}, Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.checkHealth)
	httpapi.Register(server, huma.Operation{
		OperationID: "listAccountHealth", Method: http.MethodGet,
		Path: "/v1/health/accounts", Summary: "Organisation account health summary",
		Description: "Diagnoses every account of the active organisation and returns the severity counts plus a page " +
			"of rows (name, state, severity, one-line summary, hint codes). Filter by `severity` and `q` (name or " +
			"email); sort by `severity`, `name`, `state` or `lastSyncAt` (prefix `-` for descending); `page` / " +
			"`limit` (default 50, max 200). API keys limited to some accounts see only those (rows and counts). The " +
			"organisation's diagnosis is cached for up to 15 seconds (a live check refreshes it); the per-account " +
			"`/v1/accounts/{accountId}/health` endpoints are always live. Any member (`read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeValidationFailed),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"counts": map[string]any{"ok": 38, "info": 3, "warning": 2, "critical": 1, "total": 44},
			"data": []any{
				map[string]any{"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "name": "Reception", "email": "reception@example.com",
					"state": "authError", "severity": "critical", "summary": "Login rejected: the provider requires an app password.",
					"hintCodes": []any{"app_password_required"}, "lastSyncAt": "2026-10-26T18:40:02Z", "lastErrorAt": "2026-10-27T07:12:40Z"},
				map[string]any{"accountId": "acc_01m5cxsea0fk66qqqft56j4zm7", "name": "Billing", "email": "billing@example.com",
					"state": "connected", "severity": "warning", "summary": "Mailbox is 92 % full.",
					"hintCodes": []any{"mailbox_full"}, "lastSyncAt": "2026-10-27T10:03:51Z"},
			},
			"total": 6, "page": 1, "limit": 2,
		}}}}},
		Tags: []string{"health"}, Extensions: httpapi.WithScope(phase, "accounts:read"),
	}, m.listHealth)
}

// accountHealthExample is a realistic diagnosis (documentation only); live adds the liveCheck block.
func accountHealthExample(live bool) map[string]any {
	h := map[string]any{
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "name": "Reception", "email": "reception@example.com",
		"state":       "authError",
		"stateReason": map[string]any{"kind": "auth", "code": "mail_auth_failed", "message": "Login failed: invalid credentials.", "at": "2026-10-27T07:12:40Z"},
		"severity":    "critical",
		"summary":     "Login rejected: the provider requires an app password.",
		"hints": []any{map[string]any{"code": "app_password_required", "severity": "critical", "title": "The provider requires an app password",
			"detail":   "Create an app password in the Google account security settings and use it instead of the normal password.",
			"docsPath": "/docs/troubleshooting/app-passwords"}},
		"facts": map[string]any{"tlsMode": "tls", "authUser": "reception@example.com", "hasPassword": true, "oauthLinked": false,
			"provider": "gmail", "idle": true, "condstore": true, "qresync": false, "authMechanisms": []any{"PLAIN", "XOAUTH2"},
			"folderCount": 14, "lastSyncAt": "2026-10-26T18:40:02Z", "lastErrorAt": "2026-10-27T07:12:40Z"},
		"recentErrors": []any{map[string]any{"at": "2026-10-27T07:12:40Z", "kind": "auth", "code": "mail_auth_failed", "protocol": "imap",
			"response": "NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)", "hint": "app_password_required"}},
		"checkedAt": "2026-10-27T10:05:00Z",
	}
	if live {
		h["liveCheck"] = map[string]any{"ok": false, "latencyMs": 412, "capabilities": []any{"IMAP4rev1", "IDLE", "CONDSTORE", "QUOTA"},
			"error": map[string]any{"code": "mail_auth_failed", "kind": "auth", "message": "The IMAP server rejected the login."}}
	}
	return h
}

type accountPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

type healthOut struct {
	Body AccountHealth
}

func (m *Module) getHealth(ctx context.Context, in *accountPath) (*healthOut, error) {
	h, err := m.svc.Get(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &healthOut{Body: *h}, nil
}

func (m *Module) checkHealth(ctx context.Context, in *accountPath) (*healthOut, error) {
	h, err := m.svc.Check(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &healthOut{Body: *h}, nil
}

type listIn struct {
	Page         int    `query:"page" doc:"1-based page number." example:"1"`
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total." example:"true"`
	Sort         string `query:"sort" doc:"severity, name, state or lastSyncAt; prefix - for descending." example:"-severity"`
	Severity     string `query:"severity" doc:"Only rows with this severity: ok, info, warning or critical." example:"critical"`
	Q            string `query:"q" doc:"Search in account name and email." example:"reception"`
}

type listOut struct {
	Body List
}

func (m *Module) listHealth(ctx context.Context, in *listIn) (*listOut, error) {
	var sev Severity
	if in.Severity != "" {
		switch Severity(in.Severity) {
		case SeverityOK, SeverityInfo, SeverityWarning, SeverityCritical:
			sev = Severity(in.Severity)
		default:
			return nil, errs.New(errs.CodeValidationFailed, "Unknown severity filter.").
				WithSafe("severity", in.Severity)
		}
	}
	if in.Sort != "" {
		switch in.Sort {
		case "severity", "-severity", "name", "-name", "state", "-state", "lastSyncAt", "-lastSyncAt":
		default:
			return nil, errs.New(errs.CodeValidationFailed, "Unknown sort key.").
				WithSafe("sort", in.Sort)
		}
	}
	l, err := m.svc.List(ctx, ListFilter{
		Page: in.Page, Limit: in.Limit, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Severity: sev, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *l}, nil
}
