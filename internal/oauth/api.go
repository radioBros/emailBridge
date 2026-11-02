package oauth

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
		OperationID: "listOauthApps", Method: http.MethodGet, Path: "/v1/oauth-apps",
		Summary: "List OAuth apps", Tags: []string{"oauth-apps"},
		Description: "Lists the OAuth apps of the active organisation (client secrets and service keys are never " +
			"returned; `hasClientSecret` / `hasServiceKey` say whether one is stored). Requires the `read` permission " +
			"(every role; API keys `oauth:read`). `q` matches part of the name (case-insensitive, ≤ 200 characters). " +
			"Pagination: `limit` (default 50, max 200) with `cursor` from `nextCursor`, or `page` with " +
			"`includeTotal=true`. Sort `createdAt` or `name` (prefix `-` for descending; default `-createdAt`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleAppList())}},
		Extensions: httpapi.WithScope(phase, "oauth:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "createOauthApp", Method: http.MethodPost, Path: "/v1/oauth-apps",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create OAuth app", Tags: []string{"oauth-apps"},
		Description: "Registers the OAuth client EmailBridge uses to connect mailboxes of one provider: `gmail`, `outlook` " +
			"and `mailru` come with endpoint and scope presets, `generic` needs `authorizeUrl`, `tokenUrl` and `scopes`. " +
			"Register `{EB_PUBLIC_URL}/callbacks/{provider}` as the redirect URI at the provider. An outlook app can be " +
			"`appOnly` (client credentials) and a gmail app can carry a service-account `serviceKey`; such apps connect " +
			"mailboxes without user consent. Secrets are encrypted at rest and never returned. At most 50 apps per " +
			"organisation; names are unique (`409 oauth_app_name_taken`). Requires `accounts.write` (owner, admin, " +
			"operator; API keys `accounts:write`). Writes an `oauthApp.created` audit record. Supports `Idempotency-Key`; " +
			"returns `Location` and `ETag`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed, errs.CodeOauthAppNameTaken, errs.CodeIdempotencyKeyReused),
		RequestBody: &huma.RequestBody{Content: jsonExamples(createAppRequestExamples())},
		Responses:   map[string]*huma.Response{"201": {Content: jsonContent(exampleOutlookApp())}},
		Extensions:  httpapi.WithScope(phase, "accounts:write"),
	}, m.create)
	httpapi.Register(server, huma.Operation{
		OperationID: "getOauthApp", Method: http.MethodGet, Path: "/v1/oauth-apps/{appId}",
		Summary: "Get OAuth app", Tags: []string{"oauth-apps"},
		Description: "Returns one OAuth app with its effective endpoints and scopes (presets resolved), without secrets, " +
			"and its `ETag`. Requires the `read` permission (API keys `oauth:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleOutlookApp())}},
		Extensions: httpapi.WithScope(phase, "oauth:read"),
	}, m.get)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchOauthApp", Method: http.MethodPatch, Path: "/v1/oauth-apps/{appId}",
		Summary: "Update OAuth app", Tags: []string{"oauth-apps"},
		Description: "Changes an OAuth app; omitted fields stay unchanged, `clientSecret` / `serviceKey` replace the stored " +
			"secret (rotate them here before they expire at the provider). `enabled: false` stops new connections and " +
			"token refreshes for every account using the app. The provider cannot be changed. Requires `accounts.write` " +
			"(API keys `accounts:write`). Send the last `ETag` as `If-Match` (`412 version_mismatch` when stale). Writes " +
			"an `oauthApp.updated` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeOauthAppNameTaken, errs.CodeVersionMismatch),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			//nolint:gosec // G101: documentation example client secret, not a real credential.
			"rotateSecret": {Summary: "Rotate the client secret", Value: map[string]any{"clientSecret": "Nf3~pQ8.aV5tW2kM7xS4bL9hJ6rC1eY0dG"}},
			"disable":      {Summary: "Disable the app", Value: map[string]any{"enabled": false}},
		})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleOutlookApp())}},
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.patch)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteOauthApp", Method: http.MethodDelete, Path: "/v1/oauth-apps/{appId}",
		Summary: "Delete OAuth app", Tags: []string{"oauth-apps"},
		Description: "Deletes an OAuth app and its encrypted secrets; answers `204`. Accounts whose `oauth.appId` still " +
			"references it are refused with `409 oauth_app_in_use` (detail includes `count`): disconnect them first. " +
			"Requires `accounts.write` (API keys `accounts:write`); `If-Match` is honoured (`412 version_mismatch`). " +
			"Writes an `oauthApp.deleted` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeOauthAppInUse, errs.CodeVersionMismatch),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.delete)
	httpapi.Register(server, huma.Operation{
		OperationID: "verifyOauthApp", Method: http.MethodPost, Path: "/v1/oauth-apps/{appId}/verify",
		Summary: "Verify OAuth app", Tags: []string{"oauth-apps"},
		Description: "Checks that the app's authorization and token endpoints are reachable from EmailBridge (through the " +
			"egress policy, 15 s timeout). It does not prove the client credentials are valid: that shows on the first " +
			"connect. The answer is `200` with `ok: false` and `code` when an endpoint is unreachable. Requires " +
			"`accounts.write` (API keys `accounts:write`). Writes an `oauthApp.verified` audit record. No request body.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"ok": {Summary: "Both endpoints reachable", Value: map[string]any{
				"ok": true, "latencyMs": 184, "authorizeReachable": true, "tokenReachable": true,
			}},
			"unreachable": {Summary: "Token endpoint unreachable", Value: map[string]any{
				"ok": false, "latencyMs": 15002, "authorizeReachable": true, "tokenReachable": false,
				"code": "oauth_exchange_failed", "detail": "One or more OAuth endpoints were unreachable.",
			}},
		})}},
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.verify)
	httpapi.Register(server, huma.Operation{
		OperationID: "startAccountOauth", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/oauth/start",
		Summary: "Start account OAuth", Tags: []string{"oauth-apps"},
		Description: "Starts the authorization-code flow (with PKCE) that connects an account's mailbox to an OAuth app: " +
			"returns the provider consent URL to open in the mailbox owner's browser. After consent the provider " +
			"redirects to `/callbacks/{provider}`, EmailBridge stores the encrypted tokens and sends the browser to the " +
			"admin account page. The state expires after 10 minutes. The app must be enabled (`422 oauth_app_disabled`) " +
			"and match the account's provider (`409 oauth_provider_mismatch`). To let the owner connect without an " +
			"admin session, create a connect link instead. Requires `accounts.write` (API keys `accounts:write`).",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeOauthAppDisabled, errs.CodeOauthProviderMismatch,
			errs.CodeServiceUnavailable),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.start)
	httpapi.Register(server, huma.Operation{
		OperationID: "getAccountOauth", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/oauth",
		Summary: "Account OAuth view", Tags: []string{"oauth-apps"},
		Description: "Says whether the account is connected with OAuth, through which app and mode, which scopes were " +
			"granted and when the current access token expires (it is refreshed automatically). Tokens are never " +
			"returned here (see `GET …/oauth-token`). A password account answers `connected: false`. Requires the " +
			"`read` permission (API keys `accounts:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Extensions: httpapi.WithScope(phase, "accounts:read"),
	}, m.getAccountOauth)
	httpapi.Register(server, huma.Operation{
		OperationID: "disconnectAccountOauth", Method: http.MethodDelete, Path: "/v1/accounts/{accountId}/oauth",
		Summary: "Disconnect account OAuth", Tags: []string{"oauth-apps"},
		Description: "Removes the account's OAuth tokens (revoking the refresh token at the provider when the app has a " +
			"revocation endpoint) and answers `204`. The account cannot log in to its mailbox again until it is " +
			"reconnected or given a password. `409 oauth_not_connected` when no OAuth link exists. Requires " +
			"`accounts.write` (API keys `accounts:write`). Writes an `account.oauth.disconnected` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeOauthNotConnected),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.disconnect)
	httpapi.Register(server, huma.Operation{
		OperationID: "exportAccountOauthToken", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/oauth-token",
		Summary: "Export OAuth access token", Tags: []string{"oauth-apps"},
		Description: "Returns a current access token of the account (refreshed first when needed) so another system can " +
			"use the mailbox directly with XOAUTH2 or the provider API; the refresh token never leaves EmailBridge. Off by " +
			"default: needs the `oauth.tokenExport` switch (organisation or account; `403 feature_disabled` otherwise). " +
			"Requires the `oauth.token.export` permission (owners and admins; API keys need the explicit " +
			"`oauth:token:export` scope). Limited to 30 exports per hour per account. Sent with `Cache-Control: no-store`; " +
			"writes an `account.oauth.token_export` audit record. `409 oauth_not_connected` for non-OAuth accounts.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeFeatureDisabled, errs.CodeNotFound, errs.CodeOauthNotConnected, errs.CodeOauthRevoked,
			errs.CodeRateLimited),
		Extensions: httpapi.WithScope(phase, "oauth:token:export"),
	}, m.exportToken)
	httpapi.Register(server, huma.Operation{
		OperationID: "listAccountServerSignatures", Method: http.MethodGet, Path: "/v1/accounts/{accountId}/server-signatures",
		Summary: "List server signatures", Tags: []string{"oauth-apps"},
		Description: "Reads the signatures configured at the mail provider, for reuse in compose: Gmail send-as " +
			"signatures for `gmail` accounts and IMAP accounts connected through a Gmail OAuth app. Microsoft Graph does " +
			"not expose signatures, so `graph` accounts answer an empty list with `reason`. Other accounts get " +
			"`400 unsupported_for_provider`. Requires the `read` permission (API keys `accounts:read`). Read-only; calls " +
			"the provider API on every request.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeUnsupportedForProvider, errs.CodeOauthRevoked, errs.CodeServiceUnavailable),
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"gmail": {Summary: "Gmail send-as signatures", Value: map[string]any{"data": []any{
				map[string]any{
					"email": "sales@example.org", "displayName": "Acme Sales", "isDefault": true, "isPrimary": true,
					"signature": "<div>Anna Verdi<br>Acme Sales · +39 02 1234 5678</div>",
				},
				map[string]any{
					"email": "offers@example.org", "displayName": "Acme Offers",
					"signature": "<div>Acme Offers team<br><a href=\"https://example.org\">example.org</a></div>",
				},
			}}},
			"graph": {Summary: "Microsoft Graph account", Value: map[string]any{"data": []any{}, "reason": "graph_signatures_unavailable"}},
		})}},
		Extensions: httpapi.WithScope(phase, "accounts:read"),
	}, m.serverSignatures)
	httpapi.Register(server, huma.Operation{
		OperationID: "createConnectLink", Method: http.MethodPost, Path: "/v1/connect-links",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create connect link", Tags: []string{"connect-links"},
		Description: "Creates a one-time link that lets the mailbox owner connect an existing account with OAuth without " +
			"an EmailBridge login: opening it redirects to the provider consent page (`GET /v1/connect-links/{token}/redeem`). " +
			"The URL contains the secret and is shown only in this response; only its hash is stored. Lifetime " +
			"`ttlSeconds` (default 1 hour, 1 minute – 7 days). The app must be enabled and match the account's provider. " +
			"Requires `accounts.write` (API keys `accounts:write`). Writes a `connectLink.created` audit record. Supports " +
			"`Idempotency-Key`. For the hosted connect page that also creates the account, see `POST /v1/connect-links/hosted`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeOauthAppDisabled, errs.CodeOauthProviderMismatch,
			errs.CodeIdempotencyKeyReused),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.createLink)
	m.documentPublic(server)
}

// documentPublic documents the raw browser routes mounted by mountPublic (W70).
func (m *Module) documentPublic(server *httpapi.Server) {
	redirect := func(desc, example string) *huma.Response {
		return &huma.Response{Description: desc, Headers: map[string]*huma.Param{
			"Location": {Description: "Where the browser goes next.", Schema: &huma.Schema{Type: "string"}, Example: example},
		}}
	}
	server.Document(&huma.Operation{
		OperationID: "redeemConnectLink", Method: http.MethodGet, Path: "/v1/connect-links/{token}/redeem",
		Summary: "Open a connect link", Tags: []string{"connect-links"},
		Description: "Browser endpoint behind the URL from `POST /v1/connect-links`. Public (the token is the " +
			"credential): validates the token, marks the link used and redirects (`302`) to the provider consent page; " +
			"after consent the provider returns to `/callbacks/{provider}`. An unknown, used or expired token is " +
			"`404 connect_link_invalid`; a disabled app `422 oauth_app_disabled` (the link is used up by then).",
		Parameters: []*huma.Param{{
			Name: "token", In: "path", Required: true,
			Description: "The `<linkId>.<secret>` part of the connect link URL.",
			Schema:      &huma.Schema{Type: "string"}, Example: "lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d",
		}},
		Security: []map[string][]string{},
		Responses: map[string]*huma.Response{"302": redirect("Redirect to the provider consent page.",
			"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?client_id=3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13&response_type=code&state=Zk9mR2xQ7tV4wY1bN6cH8jL3")},
		Metadata: httpapi.Problems(errs.CodeConnectLinkInvalid, errs.CodeOauthAppDisabled, errs.CodeOauthProviderMismatch,
			errs.CodeServiceUnavailable),
		Extensions: httpapi.Phase("P6"),
	})
	server.Document(&huma.Operation{
		OperationID: "oauthCallback", Method: http.MethodGet, Path: "/callbacks/{provider}",
		Summary: "OAuth provider callback", Tags: []string{"oauth-apps"},
		Description: "Redirect URI to register at the provider (`{EB_PUBLIC_URL}/callbacks/{provider}`). Public: the " +
			"provider sends the browser here with `code` and `state`; EmailBridge checks the single-use state, exchanges " +
			"the code (PKCE), stores the encrypted tokens on the account, writes an `account.oauth.connected` audit record " +
			"and redirects (`302`) to the admin account page with `?oauth=connected`. Every failure (consent denied, " +
			"expired state, exchange error) also redirects, with `?oauth_error=1`. An unknown provider is a plain `404`.",
		Parameters: []*huma.Param{
			{Name: "provider", In: "path", Required: true, Description: "gmail, outlook, mailru or generic.",
				Schema: &huma.Schema{Type: "string", Enum: []any{ProviderGmail, ProviderOutlook, ProviderMailru, ProviderGeneric}}, Example: "outlook"},
			{Name: "code", In: "query", Description: "Authorization code from the provider.",
				Schema: &huma.Schema{Type: "string"}, Example: "0.AQ8A4p2cnDoLb0-oxS0dH-ebakYc-j8"},
			{Name: "state", In: "query", Description: "The state returned by POST …/oauth/start or a connect link.",
				Schema: &huma.Schema{Type: "string"}, Example: "Zk9mR2xQ7tV4wY1bN6cH8jL3"},
			{Name: "error", In: "query", Description: "Set by the provider when the user denied consent or the request failed.",
				Schema: &huma.Schema{Type: "string"}, Example: "access_denied"},
		},
		Security: []map[string][]string{},
		Responses: map[string]*huma.Response{
			"302": redirect("Redirect to the admin account page (`?oauth=connected`, or `?oauth_error=1` on failure).",
				"https://emailbridge.example.com/accounts/acc_01m5x5dtxq189hak49x3b25a5h?oauth=connected"),
			"404": {Description: "Unknown provider (plain text, not a problem document).", Content: map[string]*huma.MediaType{
				"text/plain": {Schema: &huma.Schema{Type: "string", Description: "Go's default not-found text."}, Example: "404 page not found\n"},
			}},
		},
		Extensions: httpapi.Phase("P6"),
	})
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6Im9hcF8wMW01cDI4ODY3YmYwMW1yYWt4MDhrMGFxcCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"createdAt or name; prefix - for descending. Default -createdAt." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive part of the app name (≤ 200 characters)." example:"microsoft"`
}

type listOut struct {
	Body List
}

type createIn struct {
	Body CreateInput
}

type createOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new OAuth app." example:"/v1/oauth-apps/oap_01m55hx9fp0dr04jq9t64fvv6b"`
	ETag     string `header:"ETag" doc:"Version of the new app; send as If-Match on PATCH / DELETE." example:"\"v1\""`
	Body     View
}

type idIn struct {
	AppID string `path:"appId" doc:"OAuth app ID (oap_…)." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
}

type getOut struct {
	ETag string `header:"ETag" doc:"Current version; send as If-Match on PATCH / DELETE." example:"\"v2\""`
	Body View
}

type patchIn struct {
	AppID   string `path:"appId" doc:"OAuth app ID (oap_…)." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
	Body    PatchInput
}

type deleteIn struct {
	AppID   string `path:"appId" doc:"OAuth app ID (oap_…)." example:"oap_01m55hx9fp0dr04jq9t64fvv6b"`
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
}

type verifyOut struct {
	Body VerifyResult
}

type startIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5x5dtxq189hak49x3b25a5h"`
	Body      StartInput
}

type startOut struct {
	Body StartResult
}

type accountIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5x5dtxq189hak49x3b25a5h"`
}

type linkIn struct {
	Body ConnectLinkCreate
}

type linkOut struct {
	Status int
	Body   ConnectLinkCreated
}

func (m *Module) list(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) create(ctx context.Context, in *createIn) (*createOut, error) {
	a, err := m.svc.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &createOut{
		Status: http.StatusCreated, Location: "/v1/oauth-apps/" + a.ID,
		ETag: formatETag(a.Version), Body: ViewOf(a),
	}, nil
}

func (m *Module) get(ctx context.Context, in *idIn) (*getOut, error) {
	a, err := m.svc.Get(ctx, in.AppID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(a.Version), Body: ViewOf(a)}, nil
}

func (m *Module) patch(ctx context.Context, in *patchIn) (*getOut, error) {
	a, err := m.svc.Patch(ctx, in.AppID, in.IfMatch, in.Body)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(a.Version), Body: ViewOf(a)}, nil
}

func (m *Module) delete(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.svc.Delete(ctx, in.AppID, in.IfMatch); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

func (m *Module) verify(ctx context.Context, in *idIn) (*verifyOut, error) {
	res, err := m.svc.Verify(ctx, in.AppID)
	if err != nil {
		return nil, err
	}
	return &verifyOut{Body: *res}, nil
}

func (m *Module) start(ctx context.Context, in *startIn) (*startOut, error) {
	res, err := m.svc.Start(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &startOut{Body: *res}, nil
}

func (m *Module) disconnect(ctx context.Context, in *accountIn) (*struct{}, error) {
	if err := m.svc.Disconnect(ctx, in.AccountID); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

type oauthViewOut struct {
	Body AccountOAuthView
}

func (m *Module) getAccountOauth(ctx context.Context, in *accountIn) (*oauthViewOut, error) {
	v, err := m.svc.GetAccountOAuth(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &oauthViewOut{Body: v}, nil
}

type exportTokenOut struct {
	CacheControl string `header:"Cache-Control" doc:"Always no-store: the token must not be cached." example:"no-store"`
	Body         TokenExport
}

func (m *Module) exportToken(ctx context.Context, in *accountIn) (*exportTokenOut, error) {
	v, err := m.svc.ExportAccessToken(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &exportTokenOut{CacheControl: "no-store", Body: *v}, nil
}

type signaturesOut struct {
	Body ServerSignatures
}

func (m *Module) serverSignatures(ctx context.Context, in *accountIn) (*signaturesOut, error) {
	v, err := m.svc.ListServerSignatures(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	return &signaturesOut{Body: *v}, nil
}

func (m *Module) createLink(ctx context.Context, in *linkIn) (*linkOut, error) {
	res, err := m.svc.CreateConnectLink(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &linkOut{Status: http.StatusCreated, Body: *res}, nil
}
