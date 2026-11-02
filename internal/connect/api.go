package connect

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/errs"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P6")
	httpapi.Register(server, huma.Operation{
		OperationID: "createHostedConnectLink", Method: http.MethodPost, Path: "/v1/connect-links/hosted",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create hosted connect link", Tags: []string{"connect-links"},
		Description: "Creates a one-time link to EmailBridge's hosted connect page, where someone without an EmailBridge " +
			"login adds their own mailbox to the organisation: with email + password (settings found by autoconfig, " +
			"both logins tested) or with one of the organisation's OAuth apps. The page uses the organisation branding " +
			"(`/v1/connect/branding`). The URL contains the secret and is shown only in this response (only a hash is " +
			"stored). Lifetime `ttlSeconds` (default 1 hour, 1 minute – 7 days); `returnUrl` must be https on the " +
			"EmailBridge public host. Needs the `connect.hostedPage` switch (`403 feature_disabled`). Requires " +
			"`accounts.write` (owner, admin, operator; API keys `accounts:write`). Writes a `connectLink.created` audit " +
			"record. Supports `Idempotency-Key`.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The hosted connect page is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The OAuth app does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "returnUrl must be https (or http on localhost)."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.createHosted)
	httpapi.Register(server, huma.Operation{
		OperationID: "getPublicConnect", Method: http.MethodGet, Path: "/v1/public/connect/{token}",
		Summary: "Bootstrap hosted connect page", Tags: []string{"connect-links"},
		Description: "Public (the token is the credential). Called by the hosted connect page on load: validates the link " +
			"and returns the organisation branding, the enabled OAuth apps to offer and a `csrf` form token for the next " +
			"calls (valid 30 minutes). Does not use up the link. An unknown, used or expired token is " +
			"`404 connect_link_invalid`; a hosted link of an organisation that switched the page off is " +
			"`403 feature_disabled`. Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeConnectLinkInvalid},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The hosted connect page is disabled for this organisation."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleBootstrap())}},
		Extensions: phase,
	}, m.bootstrap)
	httpapi.Register(server, huma.Operation{
		OperationID: "publicConnectDiscover", Method: http.MethodPost, Path: "/v1/public/connect/{token}/discover",
		Summary: "Public autoconfig for connect page", Tags: []string{"connect-links"},
		Description: "Public (token + `csrf`). Looks up the IMAP and SMTP settings for the email address (built-in provider " +
			"list, Mozilla ISPDB, Autodiscover, SRV and MX records) so the page can prefill the form, and hints whether " +
			"the provider prefers OAuth. Nothing is stored. Limited to 20 lookups per hour per link (`429 rate_limited`). " +
			"`403 connect_csrf_invalid` for a missing or expired form token.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeConnectLinkInvalid},
			httpapi.ProblemDoc{Code: errs.CodeConnectCSRFInvalid},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many connect attempts; try again later."},
			httpapi.ProblemDoc{Code: errs.CodeAutoconfigFailed}),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{
			"email": "maria.rossi@example.com", "domain": "example.com", "source": "ispdb",
			"imap": map[string]any{"host": "imap.example.com", "port": 993, "tls": "implicit", "user": "%EMAIL%"},
			"smtp": map[string]any{"host": "smtp.example.com", "port": 465, "tls": "implicit", "user": "%EMAIL%"},
		})}},
		Extensions: phase,
	}, m.discover)
	documentBodyFields(server, http.MethodPost, "/v1/public/connect/{token}/discover", map[string]bodyFieldDoc{
		"email": {doc: "Mailbox address to look up.", example: "maria.rossi@example.com"},
		"csrf":  {doc: "Form token from GET /v1/public/connect/{token} (valid 30 minutes).", example: "1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w"},
	})
	httpapi.Register(server, huma.Operation{
		OperationID: "publicConnectPassword", Method: http.MethodPost, Path: "/v1/public/connect/{token}/password",
		Summary: "Connect with password", Tags: []string{"connect-links"},
		Description: "Public (token + `csrf`). Creates the account in the link's organisation from an email address and " +
			"password: the IMAP / SMTP settings come from the form (as returned by discover) or are discovered again, " +
			"both logins are tested first (`502 mail_auth_failed` … when one fails), the password is stored encrypted " +
			"and the mailbox identity is pinned. Uses up the link and returns the new account ID and, when the link has " +
			"a `returnUrl`, where to send the browser. Emits `account.added`. Limited to 10 attempts per hour per link. " +
			"Needs the `connect.hostedPage` switch.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeConnectLinkInvalid},
			httpapi.ProblemDoc{Code: errs.CodeConnectCSRFInvalid}, httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "IMAP and SMTP settings are required (run discover first)."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many connect attempts; try again later."},
			httpapi.ProblemDoc{Code: errs.CodeQuotaAccountsExceeded},
			httpapi.ProblemDoc{Code: errs.CodeMailAuthFailed, Detail: "IMAP verification failed."},
			httpapi.ProblemDoc{Code: errs.CodeMailConnectFailed}, httpapi.ProblemDoc{Code: errs.CodeMailTLSFailed},
			httpapi.ProblemDoc{Code: errs.CodeMailTimeout}),
		RequestBody: &huma.RequestBody{Content: jsonContent(map[string]any{
			"email": "maria.rossi@example.com", "password": "app-password-from-provider", "name": "Maria Rossi",
			"csrf": "1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w",
			"imap": map[string]any{"host": "imap.example.com", "port": 993, "tls": "implicit", "user": "maria.rossi@example.com"},
			"smtp": map[string]any{"host": "smtp.example.com", "port": 465, "tls": "implicit", "user": "maria.rossi@example.com"},
		})},
		Extensions: phase,
	}, m.password)
	httpapi.Register(server, huma.Operation{
		OperationID: "publicConnectOauthStart", Method: http.MethodPost, Path: "/v1/public/connect/{token}/oauth/start",
		Summary: "Start OAuth from connect page", Tags: []string{"connect-links"},
		Description: "Public (token + `csrf`). Registers the mailbox as an account of the link's organisation (no " +
			"password) and returns the provider consent URL for the chosen OAuth app; after consent the provider " +
			"returns to `/callbacks/{provider}`, which stores the tokens. `appId` defaults to the app preselected on the " +
			"link. Limited to 10 attempts per hour per link. The app must be enabled and match the provider " +
			"(`422 oauth_app_disabled`, `409 oauth_provider_mismatch`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeConnectLinkInvalid},
			httpapi.ProblemDoc{Code: errs.CodeConnectCSRFInvalid},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "appId is required."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many connect attempts; try again later."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The OAuth app does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeOauthAppDisabled}, httpapi.ProblemDoc{Code: errs.CodeOauthProviderMismatch},
			httpapi.ProblemDoc{Code: errs.CodeQuotaAccountsExceeded}),
		Extensions: phase,
	}, m.oauthStart)
	httpapi.Register(server, huma.Operation{
		OperationID: "repinAccountIdentity", Method: http.MethodPost, Path: "/v1/accounts/{accountId}/identity/repin",
		Summary: "Re-pin account identity", Tags: []string{"accounts"},
		Description: "Replaces the mailbox identity pinned on the account (email, IMAP user, OAuth subject). EmailBridge " +
			"refuses credential changes and OAuth logins that reach a different mailbox than the pinned one " +
			"(`409 identity_mismatch`); re-pin when the mailbox really moved, e.g. after a rename at the provider. " +
			"Omitted `email` keeps the account email. Requires `accounts.write` (API keys `accounts:write`). Returns the " +
			"new pin.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeConflict),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.repin)
	httpapi.Register(server, huma.Operation{
		OperationID: "getConnectBranding", Method: http.MethodGet, Path: "/v1/connect/branding",
		Summary: "Get organisation hosted-page branding", Tags: []string{"connect-links"},
		Description: "Returns the branding of the active organisation's hosted connect page: brand name, logos and " +
			"favicon (asset IDs and their public URLs), colours / palette and the sanitised custom header and head HTML, " +
			"with the `ETag` to send as `If-Match` on `PATCH`. Requires the `read` permission (API keys " +
			"`settings:read`). Read-only.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleBranding())}},
		Extensions: httpapi.WithScope(phase, "settings:read"),
	}, m.getBranding)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchConnectBranding", Method: http.MethodPatch, Path: "/v1/connect/branding",
		Summary: "Update organisation hosted-page branding", Tags: []string{"connect-links"},
		Description: "Changes the hosted connect page branding; omitted fields stay unchanged and the palette is merged. " +
			"Asset IDs must name uploaded assets of the matching kind (`422 branding_asset_kind`); colours are #RRGGBB " +
			"and foreground / background pairs must reach WCAG AA 4.5:1 (`422 branding_palette_contrast`). " +
			"`headerHtml` / `headHtml` are sanitised before they are stored (use `POST …/preview` to see the result). " +
			"Requires both `settings.write` and `accounts.write` (owner, admin; API keys `settings:write` + " +
			"`accounts:write`). Send the last `ETag` as `If-Match` (`412 version_mismatch`). Writes an " +
			"`organization.branding.updated` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The branding asset does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Palette colours must be #RRGGBB hex."},
			httpapi.ProblemDoc{Code: errs.CodeBrandingAssetKind}, httpapi.ProblemDoc{Code: errs.CodeBrandingPaletteContrast},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}, httpapi.ProblemDoc{Code: errs.CodeConflict}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"rebrand": {Summary: "Name, logo and colours", Value: map[string]any{
				"brandName": "Acme Mail", "logoAssetId": "bas_01m5f3vqwgczpd0cmjwqfwh974", "primaryColor": "#1D4ED8",
				"palette": map[string]any{"dark": map[string]any{"primary": "#93C5FD", "on-primary": "#0B1220"}},
			}},
			"header": {Summary: "Custom header HTML", Value: map[string]any{
				"headerHtml": "<div><p>Connect your mailbox to <strong>Acme Mail</strong></p></div>",
			}},
		})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleBranding())}},
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.patchBranding)
	httpapi.Register(server, huma.Operation{
		OperationID: "previewConnectBranding", Method: http.MethodPost, Path: "/v1/connect/branding/preview",
		Summary: "Sanitise hosted-page HTML without saving", Tags: []string{"connect-links"},
		Description: "Runs the hosted-page HTML sanitiser on `headerHtml` / `headHtml` and returns what would be stored, " +
			"without saving: scripts, event handlers, forms, `@import` and CSS expressions are removed, only allow-listed " +
			"tags and attributes stay. Requires `accounts.write` (API keys `accounts:write`).",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Extensions: httpapi.WithScope(phase, "accounts:write"),
	}, m.previewBranding)
	httpapi.Register(server, huma.Operation{
		OperationID: "listConnectBrandingAssets", Method: http.MethodGet, Path: "/v1/connect/branding/assets",
		Summary: "List organisation branding assets", Tags: []string{"connect-links"},
		Description: "Lists the images uploaded for the hosted connect page (at most 50 per organisation, 20 MiB in " +
			"total), optionally only one `kind`, each with its public URL. Requires the `read` permission (API keys " +
			"`settings:read`). Not paginated. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses: map[string]*huma.Response{"200": {Content: jsonContent(map[string]any{"data": []any{
			exampleAsset("bas_01m5f3vqwgczpd0cmjwqfwh974", "logo", "image/png", 480, 120, 18342),
			exampleAsset("bas_01m57srhxbmqd4ybm793jpc5af", "favicon", "image/x-icon", 48, 48, 4286),
		}})}},
		Extensions: httpapi.WithScope(phase, "settings:read"),
	}, m.listAssets)
	httpapi.Register(server, huma.Operation{
		OperationID: "uploadConnectBrandingAsset", Method: http.MethodPost, Path: "/v1/connect/branding/assets",
		DefaultStatus: http.StatusCreated,
		Summary:       "Upload a branding image", Tags: []string{"connect-links"},
		Description: "Uploads an image for the hosted connect page as `multipart/form-data` (`file` + `kind`). PNG, JPEG, " +
			"WebP, GIF and ICO are accepted, SVG never (`415 branding_asset_type`); logos and images ≤ 2 MiB and 4096 px " +
			"per side, favicons ≤ 256 KiB and 512 px (`413` / `422 branding_asset_dimensions`). With antivirus on, the " +
			"file is scanned first (`403 attachment_infected`). Quota: 50 assets / 20 MiB per organisation " +
			"(`422 branding_asset_quota`). Reference the returned ID in `PATCH /v1/connect/branding`. Requires " +
			"`settings.write` (owner, admin; API keys `settings:write`). Writes an `organization.brandingAsset.created` " +
			"audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "kind must be logo, logoDark, favicon or image."},
			httpapi.ProblemDoc{Code: errs.CodePayloadTooLarge, Detail: "The image exceeds the size limit for this kind."},
			httpapi.ProblemDoc{Code: errs.CodeBrandingAssetType, Detail: "SVG is not allowed for branding assets."},
			httpapi.ProblemDoc{Code: errs.CodeBrandingAssetDimensions}, httpapi.ProblemDoc{Code: errs.CodeBrandingAssetQuota},
			httpapi.ProblemDoc{Code: errs.CodeAttachmentInfected}, httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable}),
		Responses: map[string]*huma.Response{"201": {Content: jsonContent(
			exampleAsset("bas_01m5f3vqwgczpd0cmjwqfwh974", "logo", "image/png", 480, 120, 18342))}},
		Extensions:   httpapi.WithScope(phase, "settings:write"),
		MaxBodyBytes: maxLogoBytes + (64 << 10),
	}, m.uploadAsset)
	// The multipart schema is generated by huma from the form struct; only the example is added here (W70).
	if p := server.API.OpenAPI().Paths["/v1/connect/branding/assets"]; p != nil && p.Post != nil && p.Post.RequestBody != nil {
		if mt := p.Post.RequestBody.Content["multipart/form-data"]; mt != nil {
			mt.Example = map[string]any{"kind": "logo", "file": "(binary PNG image, e.g. acme-logo.png)"}
		}
	}
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteConnectBrandingAsset", Method: http.MethodDelete, Path: "/v1/connect/branding/assets/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Delete a branding asset", Tags: []string{"connect-links"},
		Description: "Deletes an uploaded branding image and its stored object; answers `204`. An asset still referenced " +
			"by the branding (logo, dark logo, favicon) is refused with `409 branding_asset_in_use`: unset it first. " +
			"Requires `settings.write` (API keys `settings:write`). Writes an `organization.brandingAsset.deleted` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeBrandingAssetInUse),
		Extensions: httpapi.WithScope(phase, "settings:write"),
	}, m.deleteAsset)
	httpapi.Register(server, huma.Operation{
		OperationID: "getPublicBrandingAsset", Method: http.MethodGet, Path: "/v1/public/branding/assets/{id}",
		Summary: "Stream a branding asset (public)", Tags: []string{"connect-links"},
		Description: "Public: streams an uploaded branding image so the hosted page and custom header HTML can show it. " +
			"Served with its detected image type, `X-Content-Type-Options: nosniff`, a restrictive " +
			"`Content-Security-Policy` and long immutable caching (asset IDs never change content). Unknown IDs are " +
			"`404 not_found`.",
		Metadata: httpapi.Problems(errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {
			Description: "The image bytes.",
			Headers: map[string]*huma.Param{
				"Content-Type":            {Description: "Detected image type.", Schema: &huma.Schema{Type: "string"}, Example: "image/png"},
				"Cache-Control":           {Description: "Long-lived caching.", Schema: &huma.Schema{Type: "string"}, Example: "public, max-age=31536000, immutable"},
				"ETag":                    {Description: "Content hash of the image.", Schema: &huma.Schema{Type: "string"}, Example: `"9b2cf535f27731c974343645a3985328"`},
				"X-Content-Type-Options":  {Description: "Always nosniff.", Schema: &huma.Schema{Type: "string"}, Example: "nosniff"},
				"Content-Security-Policy": {Description: "Sandboxes the response.", Schema: &huma.Schema{Type: "string"}, Example: "default-src 'none'; style-src 'none'; sandbox"},
			},
			Content: map[string]*huma.MediaType{"image/*": {
				Schema:  &huma.Schema{Type: "string", Format: "binary", Description: "PNG, JPEG, WebP, GIF or ICO image."},
				Example: "(binary image data)",
			}},
		}},
		Extensions: phase,
	}, m.publicAsset)
}

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleBranding() map[string]any {
	asset := func(id string) string { return "https://emailbridge.example.com/v1/public/branding/assets/" + id }
	return map[string]any{
		"brandName": "Acme Mail", "logoAssetId": "bas_01m5f3vqwgczpd0cmjwqfwh974", "faviconAssetId": "bas_01m57srhxbmqd4ybm793jpc5af",
		"logoUrl": asset("bas_01m5f3vqwgczpd0cmjwqfwh974"), "faviconUrl": asset("bas_01m57srhxbmqd4ybm793jpc5af"),
		"primaryColor": "#1D4ED8",
		"palette": map[string]any{
			"light": map[string]any{"primary": "#1D4ED8", "on-primary": "#FFFFFF"},
			"dark":  map[string]any{"primary": "#93C5FD", "on-primary": "#0B1220"},
		},
		"headerHtml": "<div><p>Connect your mailbox to <strong>Acme Mail</strong></p></div>",
	}
}

func exampleBootstrap() map[string]any {
	return map[string]any{
		"branding": exampleBranding(), "mode": ModeHosted,
		"oauthApps": []any{
			map[string]any{"id": "oap_01m55hx9fp0dr04jq9t64fvv6b", "name": "Microsoft 365 – Acme", "provider": "outlook"},
			map[string]any{"id": "oap_01m5p28867bf01mrakx08k0aqp", "name": "Google Workspace – Acme", "provider": "gmail"},
		},
		"csrf":      "1793097000.Jq2mV7tZ1xR9pW3bN6cL0dH4jF5gY2sA8vB1nM7kQ3w",
		"expiresAt": "2026-10-28T09:30:00Z",
	}
}

func exampleAsset(id, kind, ct string, w, h int, size int64) map[string]any {
	return map[string]any{
		"id": id, "kind": kind, "contentType": ct, "width": w, "height": h, "size": size,
		"createdAt": "2026-10-26T15:12:40Z", "url": "https://emailbridge.example.com/v1/public/branding/assets/" + id,
	}
}

type createHostedIn struct {
	Body HostedCreate
}
type createHostedOut struct {
	Body LinkCreated
}

func (m *Module) createHosted(ctx context.Context, in *createHostedIn) (*createHostedOut, error) {
	out, err := m.svc.CreateHosted(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &createHostedOut{Body: *out}, nil
}

type tokenIn struct {
	Token string `path:"token" doc:"The <linkId>.<secret> part of the connect link URL (/connect/<token>)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
}
type bootstrapOut struct {
	Body Bootstrap
}

func (m *Module) bootstrap(ctx context.Context, in *tokenIn) (*bootstrapOut, error) {
	out, err := m.svc.Bootstrap(ctx, in.Token)
	if err != nil {
		return nil, err
	}
	return &bootstrapOut{Body: *out}, nil
}

type discoverIn struct {
	Token string `path:"token" doc:"The <linkId>.<secret> part of the connect link URL (/connect/<token>)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
	Body  struct {
		Email string `json:"email"`
		CSRF  string `json:"csrf"`
	}
}
type discoverOut struct {
	Body *autoconfig.Result
}

func (m *Module) discover(ctx context.Context, in *discoverIn) (*discoverOut, error) {
	out, err := m.svc.Discover(ctx, in.Token, in.Body.Email, in.Body.CSRF)
	if err != nil {
		return nil, err
	}
	return &discoverOut{Body: out}, nil
}

type passwordIn struct {
	Token string `path:"token" doc:"The <linkId>.<secret> part of the connect link URL (/connect/<token>)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
	Body  PasswordInput
}
type passwordOut struct {
	Body Done
}

func (m *Module) password(ctx context.Context, in *passwordIn) (*passwordOut, error) {
	out, err := m.svc.PasswordConnect(ctx, in.Token, in.Body)
	if err != nil {
		return nil, err
	}
	return &passwordOut{Body: *out}, nil
}

type oauthStartIn struct {
	Token string `path:"token" doc:"The <linkId>.<secret> part of the connect link URL (/connect/<token>)." example:"lnk_01m5bzn1mw96t2fy8shqbd87jm.R7tK2pW9xQ4mV8nB3cL6yH1d"`
	Body  OAuthStartInput
}
type oauthStartOut struct {
	Body OAuthStartResult
}

func (m *Module) oauthStart(ctx context.Context, in *oauthStartIn) (*oauthStartOut, error) {
	out, err := m.svc.OAuthStartPublic(ctx, in.Token, in.Body)
	if err != nil {
		return nil, err
	}
	return &oauthStartOut{Body: *out}, nil
}

type repinIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Body      RepinInput
}
type repinOut struct {
	Body Identity
}

func (m *Module) repin(ctx context.Context, in *repinIn) (*repinOut, error) {
	out, err := m.svc.Repin(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &repinOut{Body: *out}, nil
}

type brandingOut struct {
	ETag string `header:"ETag" doc:"Version of the organisation branding; send as If-Match on PATCH." example:"\"5\""`
	Body Branding
}

func (m *Module) getBranding(ctx context.Context, _ *struct{}) (*brandingOut, error) {
	b, ver, err := m.svc.GetBranding(ctx)
	if err != nil {
		return nil, err
	}
	return &brandingOut{ETag: `"` + formatVer(ver) + `"`, Body: *b}, nil
}

type patchBrandingIn struct {
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"5\""`
	Body    BrandingUpdate
}

func (m *Module) patchBranding(ctx context.Context, in *patchBrandingIn) (*brandingOut, error) {
	b, ver, err := m.svc.UpdateBranding(ctx, in.Body, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &brandingOut{ETag: `"` + formatVer(ver) + `"`, Body: *b}, nil
}

type previewBrandingIn struct {
	Body BrandingPreviewRequest
}
type previewBrandingOut struct {
	Body BrandingPreview
}

func (m *Module) previewBranding(ctx context.Context, in *previewBrandingIn) (*previewBrandingOut, error) {
	out, err := m.svc.PreviewBrandingHTML(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &previewBrandingOut{Body: *out}, nil
}

func formatVer(v int64) string {
	return strconv.FormatInt(v, 10)
}

type listAssetsIn struct {
	Kind AssetKind `query:"kind" doc:"Only assets of this kind: logo, logoDark, favicon or image." example:"logo"`
}
type listAssetsOut struct {
	Body BrandingAssetList
}

func (m *Module) listAssets(ctx context.Context, in *listAssetsIn) (*listAssetsOut, error) {
	out, err := m.svc.ListAssets(ctx, in.Kind)
	if err != nil {
		return nil, err
	}
	return &listAssetsOut{Body: *out}, nil
}

type uploadAssetIn struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"The image: PNG, JPEG, WebP, GIF or ICO (no SVG). Logos ≤ 2 MiB and 4096 px per side, favicons ≤ 256 KiB and 512 px."`
		Kind string        `form:"kind" required:"true" enum:"logo,logoDark,favicon,image" doc:"What the image is for: logo, logoDark, favicon or image (free use in headerHtml)." example:"logo"`
	}]
}
type uploadAssetOut struct {
	Body BrandingAsset
}

func (m *Module) uploadAsset(ctx context.Context, in *uploadAssetIn) (*uploadAssetOut, error) {
	data := in.RawBody.Data()
	if data == nil || !data.File.IsSet {
		return nil, errs.New(errs.CodeValidationFailed, "file is required")
	}
	raw, err := io.ReadAll(io.LimitReader(data.File, maxLogoBytes+1))
	if err != nil {
		return nil, err
	}
	_ = data.File.Close()
	out, err := m.svc.UploadAsset(ctx, AssetKind(data.Kind), raw)
	if err != nil {
		return nil, err
	}
	return &uploadAssetOut{Body: *out}, nil
}

type deleteAssetIn struct {
	ID string `path:"id" doc:"Branding asset ID (bas_…)." example:"bas_01m5f3vqwgczpd0cmjwqfwh974"`
}

func (m *Module) deleteAsset(ctx context.Context, in *deleteAssetIn) (*struct{}, error) {
	if err := m.svc.DeleteAsset(ctx, in.ID); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

type publicAssetIn struct {
	ID string `path:"id" doc:"Branding asset ID (bas_…)." example:"bas_01m5f3vqwgczpd0cmjwqfwh974"`
}

func (m *Module) publicAsset(ctx context.Context, in *publicAssetIn) (*huma.StreamResponse, error) {
	a, rc, err := m.svc.OpenPublicAsset(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer func() { _ = rc.Close() }()
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		hctx.SetHeader("Content-Type", ct)
		hctx.SetHeader("X-Content-Type-Options", "nosniff")
		hctx.SetHeader("Content-Disposition", "inline")
		hctx.SetHeader("Content-Security-Policy", "default-src 'none'; style-src 'none'; sandbox")
		hctx.SetHeader("Cache-Control", "public, max-age=31536000, immutable")
		if a.ETag != "" {
			hctx.SetHeader("ETag", `"`+a.ETag+`"`)
		}
		hctx.SetStatus(http.StatusOK)
		_, _ = io.Copy(hctx.BodyWriter(), rc)
	}}, nil
}
