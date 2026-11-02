package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P1")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccounts", Method: http.MethodGet, Path: "/v1/accounts",
		Summary: "List accounts", Tags: []string{"accounts"},
		Description: "Lists the mail accounts of the active organisation with credentials redacted (passwords are never " +
			"returned; `auth.hasPassword` says whether one is stored). Requires the `read` permission (every role; API keys " +
			"need `accounts:read`). An account-bound API key sees only its own account, a key with per-account scopes only " +
			"those accounts. Filter by `state`, by `q` (case-insensitive prefix of name, email or externalId) and by " +
			"metadata: `metadata.<key>=<value>` matches accounts whose metadata key holds exactly that string (at most 3 " +
			"such filters, all must match; keys are 1–64 characters of `[A-Za-z0-9_.-]`, a key with a dot is matched " +
			"literally; number and boolean values never match). " +
			"Pagination: `limit` (default 50, max 200) with `cursor` from `nextCursor`, or `page` (1-based) with " +
			"`includeTotal=true` for a total capped at 10 000. Sort by `name`, `email`, `state`, `createdAt`, `lastSyncAt` " +
			"or `id` (prefix `-` for descending; default `-id`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleAccountList())}},
		Extensions: httpapi.WithScope(ext, "accounts:read"),
	}, m.listAccounts)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createAccount", Method: http.MethodPost, Path: "/v1/accounts",
		DefaultStatus: http.StatusCreated,
		Summary:       "Register account", Tags: []string{"accounts"},
		Description: "Registers a mailbox in the active organisation. Unless `skipVerify` is true (or the provider is " +
			"`gmail` / `graph`), EmailBridge first logs in to IMAP and SMTP with the given settings and saves nothing " +
			"when either fails (`502 mail_auth_failed` / `mail_connect_failed` / `mail_tls_failed` / `mail_protocol_error`, " +
			"`504 mail_timeout`); the test counts against the verify limit of 5 per minute per organisation. Passwords " +
			"are encrypted at rest and never returned. `metadata` is your own free-form object (at most 32 keys, values " +
			"string ≤ 1024 bytes, number, boolean or null, ≤ 8 KB as JSON); invalid metadata is `422 validation_failed` " +
			"with `errors[].path` = `body.metadata.<key>` (or `body.metadata` for the key count and size). " +
			"A mailbox can instead authenticate through an app-only OAuth app " +
			"(`oauth.appId`), another account's OAuth token (`delegated`) or the credential server (`auth.useAuthServer`). " +
			"The account starts in state `new`; a sync node picks it up within seconds. Requires `accounts.write` " +
			"(owner, admin, operator; API keys `accounts:write`, not account-bound). Emits `account.added` and writes an " +
			"`account.create` audit record. Supports `Idempotency-Key`; returns `Location` and `ETag`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed, errs.CodeSwitchUnknown, errs.CodeDelegatedInvalid, errs.CodeOauthAppLinkInvalid,
			errs.CodeOauthAppDisabled, errs.CodeNotFound, errs.CodeExternalIDTaken, errs.CodeIdempotencyKeyReused,
			errs.CodeQuotaAccountsExceeded, errs.CodeRateLimited, errs.CodeMailAuthFailed, errs.CodeMailConnectFailed,
			errs.CodeMailTLSFailed, errs.CodeMailProtocolError, errs.CodeMailTimeout),
		RequestBody: &huma.RequestBody{Content: jsonExamples(createAccountRequestExamples())},
		Responses:   map[string]*huma.Response{"201": {Content: jsonContent(exampleNewAccountView())}},
		Extensions:  httpapi.WithScope(ext, "accounts:write"),
	}, m.createAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "verifyAccountSettings", Method: http.MethodPost, Path: "/v1/accounts/verify",
		Summary: "Test IMAP and/or SMTP without saving", Tags: []string{"accounts"},
		Description: "Connects to the given IMAP and/or SMTP server, negotiates TLS and logs in, then disconnects; nothing " +
			"is stored. Use it in a setup form before `POST /v1/accounts`. The answer is `200` even when a login fails: " +
			"each protocol reports `ok`, the latency, the server capabilities (IMAP also the detected special-use folders) " +
			"or a problem `code` with the redacted server `detail`. Each protocol test times out after 30 s. Limited to 5 " +
			"requests per minute per organisation (`429 rate_limited`). Requires `accounts.write` (API keys " +
			"`accounts:write`). Connections go through the instance egress policy (private destinations may be blocked).",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed, errs.CodeRateLimited),
		RequestBody: &huma.RequestBody{Content: jsonExamples(verifyRequestExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonExamples(verifyResponseExamples())}},
		Extensions:  httpapi.WithScope(ext, "accounts:write"),
	}, m.verifyAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccount", Method: http.MethodGet, Path: "/v1/accounts/{accountId}",
		Summary: "Get account", Tags: []string{"accounts"},
		Description: "Returns one account with credentials redacted, its connection state and the reason of the last state " +
			"change, plus the `ETag` to send as `If-Match` on `PATCH` / `DELETE`. Requires the `read` permission (API keys " +
			"`accounts:read` or `accounts:read:<accountId>`). An account of another organisation is `404 not_found`; an " +
			"account-bound key asking for another account gets `403 forbidden`. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleAccountView())}},
		Extensions: httpapi.WithScope(ext, "accounts:read"),
	}, m.getAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchAccount", Method: http.MethodPatch, Path: "/v1/accounts/{accountId}",
		Summary: "Update account", Tags: []string{"accounts"},
		Description: "Changes an account; omitted fields stay unchanged and `externalId: null` removes the external ID. " +
			"`metadata` is a JSON Merge Patch of the stored metadata: listed keys are set, a key set to `null` is removed, " +
			"`metadata: null` removes every key; the merged object must stay within the limits of `POST /v1/accounts`. " +
			"`imap` / `smtp` are sent as whole objects, but empty values keep what is stored (send `auth.pass` only to " +
			"replace the password). Changing servers, credentials or protocol-log switches queues a reconnect " +
			"(`configChanged`); a credential change that reaches a different mailbox than the pinned identity fails with " +
			"`409 identity_mismatch` (re-pin with `POST …/identity/repin`). `disabled: true` stops the account " +
			"(re-enable with `POST …/resume`); `oauth.appId` binds an app-only OAuth app. Requires `accounts.write` " +
			"(API keys `accounts:write`). Send the last `ETag` as `If-Match` to avoid lost updates (`412 version_mismatch`). " +
			"Emits `account.updated` (and `account.stateChanged` when the state moves); writes an `account.update` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeSwitchUnknown, errs.CodeExternalIDTaken,
			errs.CodeAccountDeleting, errs.CodeIdentityMismatch, errs.CodeConflict, errs.CodeVersionMismatch,
			errs.CodeOauthAppLinkInvalid),
		RequestBody: &huma.RequestBody{Content: jsonExamples(patchAccountRequestExamples())},
		Responses:   map[string]*huma.Response{"200": {Content: jsonContent(exampleAccountView())}},
		Extensions:  httpapi.WithScope(ext, "accounts:write"),
	}, m.patchAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteAccount", Method: http.MethodDelete, Path: "/v1/accounts/{accountId}",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Delete account", Tags: []string{"accounts"},
		Description: "Moves the account to `deleting`, disconnects it and queues an `erasure` job that deletes " +
			"every stored copy (messages, mailboxes, outbound queue, objects in S3, logs), emits `account.deleted` and " +
			"removes the account; the mailbox on the mail server is not touched. Answers `202` with the job and " +
			"`Location: /v1/jobs/{jobId}`; poll the job for progress. " +
			"Irreversible. An account other accounts delegate to cannot be deleted (`409 delegated_in_use`). Requires " +
			"`accounts.write` (API keys `accounts:write`); `If-Match` is honoured. Emits `account.stateChanged` " +
			"(to `deleting`) and writes an `account.delete` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeAccountDeleting, errs.CodeDelegatedInUse, errs.CodeConflict,
			errs.CodeVersionMismatch),
		Responses:  map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("erasure"))}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, m.deleteAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "syncAccount", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/sync",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Resync now", Tags: []string{"accounts"},
		Description: "Asks the sync node that owns the account to resynchronise now instead of waiting for IDLE or the " +
			"next poll: `fast` compares mailbox STATUS first and only fetches what changed, `full` diffs every UID and " +
			"flag. Optional `paths` limits it to some mailboxes. New, changed and deleted messages raise the usual " +
			"`message.*` events. Answers `202` with an `account.resync` tracking job. Requires `accounts.operate` " +
			"(owner, admin, operator; API keys `accounts:write`). Supports `Idempotency-Key`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeAccountDeleting, errs.CodeVersionMismatch),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"fast":    {Summary: "Fast resync of every mailbox", Value: map[string]any{"mode": "fast"}},
			"fullTwo": {Summary: "Full resync of two mailboxes", Value: map[string]any{"mode": "full", "paths": []any{"INBOX", "Sent"}}},
		})},
		Responses:  map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("account.resync"))}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, m.syncAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "reconnectAccount", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/reconnect",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Drop and reconnect", Tags: []string{"accounts"},
		Description: "Closes the account's IMAP connections and opens them again (for example after fixing something on " +
			"the mail server); the sync owner reconnects and reports state changes as `account.stateChanged`. Answers `202` with " +
			"an `account.reconnect` tracking job. Requires `accounts.operate` (API keys `accounts:write`). Supports " +
			"`Idempotency-Key`; no request body.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeAccountDeleting, errs.CodeVersionMismatch),
		Responses:  map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("account.reconnect"))}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, m.reconnectAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "flushAccount", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/flush",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Flush sync index and silent-resync", Tags: []string{"accounts"},
		Description: "Drops the account's local sync index and rebuilds it from the server without raising " +
			"`message.received` for messages that already existed (a silent resync). Use it when the index is out of " +
			"step with the server, e.g. after a UIDVALIDITY change or a server migration. Answers `202` with an " +
			"`account.flush` tracking job. Requires `accounts.operate` (API keys `accounts:write`). Writes an " +
			"`account.flush` audit record. Supports `Idempotency-Key`; no request body.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeAccountDeleting, errs.CodeVersionMismatch),
		Responses:  map[string]*huma.Response{"202": {Content: jsonContent(exampleJobAccepted("account.flush"))}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, m.flushAccount)
	httpapi.Register(srv, huma.Operation{
		OperationID: "resumeAccount", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/resume",
		Summary: "Re-enable a disabled account", Tags: []string{"accounts"},
		Description: "Re-enables an account that is `disabled` (by `PATCH disabled: true` or automatically after a long " +
			"run of login failures): the state moves to `connecting`, `authFailingSince` is cleared and a sync node picks " +
			"the account up again. Any other state is `409 account_not_disabled`. Requires `accounts.operate` (API keys " +
			"`accounts:write`). Emits `account.stateChanged` and writes an `account.resume` audit record. Returns the " +
			"account with its new `ETag`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeAccountNotDisabled, errs.CodeVersionMismatch),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleAccountView())}},
		Extensions: httpapi.WithScope(ext, "accounts:write"),
	}, m.resumeAccount)
}

type listIn struct {
	// metadata holds the metadata.<key>=<value> query filters; huma cannot bind dynamic names, so Resolve reads them.
	metadata     map[string]string
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImFjY18wMW01eDVkdHhxMTg5aGFrNDl4M2IyNWE1aCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"name, email, state, createdAt, lastSyncAt or id; prefix - for descending. Default -id." example:"name"`
	State        string `query:"state" doc:"Only accounts in this state: new, connecting, syncing, connected, authError, connectError, disconnected, disabled or deleting." example:"authError"`
	Q            string `query:"q" doc:"Case-insensitive prefix of the name, email or externalId." example:"recep"`
}

// Resolve collects the `metadata.<key>` query parameters (G4). The service validates keys and limits; a key
// sent twice is rejected here because only one value per key is meaningful.
func (in *listIn) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	var problems []error
	for name, values := range u.Query() {
		key, ok := strings.CutPrefix(name, "metadata.")
		if !ok {
			continue
		}
		if len(values) != 1 {
			problems = append(problems, &huma.ErrorDetail{
				Location: "query." + name, Message: "metadata filter given more than once", Value: values,
			})
			continue
		}
		if in.metadata == nil {
			in.metadata = map[string]string{}
		}
		in.metadata[key] = values[0]
	}
	return problems
}

type listOut struct {
	Body accountListBody
}

type accountListBody struct {
	Data        []AccountView `json:"data" doc:"The accounts of this page."`
	NextCursor  *string       `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6ImFjY18wMW01eDVkdHhxMTg5aGFrNDl4M2IyNWE1aCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64        `json:"total,omitempty" doc:"Total number of matching accounts (only with page and includeTotal)." example:"42"`
	TotalCapped *bool         `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

type createIn struct {
	Body CreateInput
}

type accountOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new account." example:"/v1/accounts/acc_01m5cvnpb031g57sqct6mzhq94"`
	ETag     string `header:"ETag" doc:"Version of the new account; send as If-Match on PATCH / DELETE." example:"\"v1\""`
	Body     AccountView
}

type accountIDIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

type getOut struct {
	ETag string `header:"ETag" doc:"Current version; send as If-Match on PATCH / DELETE." example:"\"v3\""`
	Body AccountView
}

type patchBody struct {
	Name       *string          `json:"name,omitempty" doc:"New display name (not blank)." example:"Front desk"`
	Email      *string          `json:"email,omitempty" doc:"New email address of the mailbox." example:"frontdesk@example.com"`
	ExternalID json.RawMessage  `json:"externalId,omitempty" doc:"New external ID (1–128 characters of [A-Za-z0-9._@+-], unique in the organisation); null removes it." example:"\"crm-48213\""`
	Provider   *string          `json:"provider,omitempty" doc:"Rejected when set: provider is immutable after create." example:"imap"`
	IMAP       *IMAPConfigInput `json:"imap,omitempty" doc:"IMAP settings; empty values keep the stored ones, auth.pass only to replace the password."`
	SMTP       *SMTPConfigInput `json:"smtp,omitempty" doc:"SMTP settings; empty values keep the stored ones, auth.pass only to replace the password."`
	Sync       *SyncConfig      `json:"sync,omitempty" doc:"Replaces the synchronisation settings."`
	TZ         *string          `json:"tz,omitempty" doc:"IANA time zone." example:"Europe/Rome"`
	Locale     *string          `json:"locale,omitempty" doc:"Language of account-related texts; one of the supported UI languages." example:"en"`
	Network    *NetworkConfig   `json:"network,omitempty" doc:"Optional dialer settings (proxy, source address, EHLO name)."`
	Switches   map[string]any   `json:"switches,omitempty" doc:"Replaces the account-level switch overrides (name → value)." example:"{\"protocolLogs.enabled\":true,\"protocolLogs.rawDebug\":false}"`
	Disabled   *bool            `json:"disabled,omitempty" doc:"true disables the account (sync stops until POST …/resume); false is ignored." example:"true"`
	RePin      bool             `json:"rePin,omitempty" doc:"Accept a new mailbox identity when IMAP/SMTP credentials change." example:"false"`
	// OAuth links the account to a service-account / app-only OAuth app (W43).
	OAuth *OAuthBindInput `json:"oauth,omitempty" doc:"Bind a service-account / app-only OAuth app."`
	Auth  *AuthInput      `json:"auth,omitempty" doc:"Credential source options (external auth server)."`
	// Delegated updates shared-mailbox binding; JSON null clears.
	Delegated json.RawMessage `json:"delegated,omitempty" doc:"Shared / delegated mailbox binding; null clears it."`
	// Metadata is a JSON Merge Patch of the account metadata (G4); JSON null removes every key.
	Metadata json.RawMessage `json:"metadata,omitempty" doc:"JSON Merge Patch of your free-form metadata: keys listed are set, a key set to null is removed, null removes all metadata. The merged object is validated like on create (≤ 32 keys, scalar values, ≤ 8 KB)." example:"{\"crmId\":\"48213\",\"legacyCode\":null}"`
}

type patchIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	IfMatch   string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v3\""`
	Body      patchBody
}

type deleteIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	IfMatch   string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v3\""`
}

type jobOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the tracking job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     JobAccepted
}

type verifyIn struct {
	Body VerifyRequest
}

type verifyOut struct {
	Body VerifyResult
}

type syncIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Body      SyncInput
}

type accountIDOnly struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

func (m *Module) listAccounts(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Sort: in.Sort, State: in.State, Q: in.Q,
		Metadata: in.metadata,
	})
	if err != nil {
		return nil, withFieldErrors(ctx, err)
	}
	views := make([]AccountView, len(res.Data))
	for i := range res.Data {
		views[i] = res.Data[i].View()
	}
	return &listOut{Body: accountListBody{
		Data: views, NextCursor: res.NextCursor, Total: res.Total, TotalCapped: res.TotalCapped,
	}}, nil
}

func (m *Module) createAccount(ctx context.Context, in *createIn) (*accountOut, error) {
	a, err := m.svc.Create(ctx, in.Body)
	if err != nil {
		return nil, withFieldErrors(ctx, err)
	}
	return &accountOut{
		Status: http.StatusCreated, Location: "/v1/accounts/" + a.ID,
		ETag: formatETag(a.Version), Body: a.View(),
	}, nil
}

func (m *Module) verifyAccount(ctx context.Context, in *verifyIn) (*verifyOut, error) {
	res, err := m.svc.Verify(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &verifyOut{Body: *res}, nil
}

func (m *Module) getAccount(ctx context.Context, in *accountIDIn) (*getOut, error) {
	a, err := m.svc.Get(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(a.Version), Body: a.View()}, nil
}

func (m *Module) patchAccount(ctx context.Context, in *patchIn) (*getOut, error) {
	pin := PatchInput{
		Name: in.Body.Name, Email: in.Body.Email, Provider: in.Body.Provider,
		IMAP: in.Body.IMAP, SMTP: in.Body.SMTP, Sync: in.Body.Sync,
		TZ: in.Body.TZ, Locale: in.Body.Locale, Network: in.Body.Network,
		Switches: in.Body.Switches, Disabled: in.Body.Disabled, RePin: in.Body.RePin,
		OAuth: in.Body.OAuth, Auth: in.Body.Auth,
	}
	if len(in.Body.ExternalID) > 0 {
		if string(in.Body.ExternalID) == "null" {
			pin.ClearExt = true
		} else {
			var s string
			if err := json.Unmarshal(in.Body.ExternalID, &s); err != nil {
				return nil, errs.New(errs.CodeValidationFailed, "externalId must be a string or null.")
			}
			pin.ExternalID = &s
		}
	}
	if len(in.Body.Delegated) > 0 {
		if string(in.Body.Delegated) == "null" {
			pin.ClearDelegated = true
		} else {
			var d DelegatedRef
			if err := json.Unmarshal(in.Body.Delegated, &d); err != nil {
				return nil, errs.New(errs.CodeValidationFailed, "delegated must be an object or null.")
			}
			pin.Delegated = &d
		}
	}
	if len(in.Body.Metadata) > 0 {
		patch, clear, err := decodeMetadataPatch(in.Body.Metadata)
		if err != nil {
			return nil, withFieldErrors(ctx, err)
		}
		pin.Metadata, pin.ClearMetadata = patch, clear
	}
	a, err := m.svc.Patch(ctx, in.AccountID, pin, in.IfMatch)
	if err != nil {
		return nil, withFieldErrors(ctx, err)
	}
	return &getOut{ETag: formatETag(a.Version), Body: a.View()}, nil
}

func (m *Module) deleteAccount(ctx context.Context, in *deleteIn) (*jobOut, error) {
	j, err := m.svc.Delete(ctx, in.AccountID, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &jobOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID,
		Body: JobAccepted{Job: j},
	}, nil
}

func (m *Module) syncAccount(ctx context.Context, in *syncIn) (*jobOut, error) {
	j, err := m.svc.Sync(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &jobOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID,
		Body: JobAccepted{Job: j},
	}, nil
}

func (m *Module) reconnectAccount(ctx context.Context, in *accountIDOnly) (*jobOut, error) {
	j, err := m.svc.Reconnect(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &jobOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID,
		Body: JobAccepted{Job: j},
	}, nil
}

func (m *Module) flushAccount(ctx context.Context, in *accountIDOnly) (*jobOut, error) {
	j, err := m.svc.Flush(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &jobOut{
		Status: http.StatusAccepted, Location: "/v1/jobs/" + j.ID,
		Body: JobAccepted{Job: j},
	}, nil
}

func (m *Module) resumeAccount(ctx context.Context, in *accountIDOnly) (*getOut, error) {
	a, err := m.svc.Resume(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(a.Version), Body: a.View()}, nil
}
