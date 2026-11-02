package settings

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	httpapi.Register(srv, huma.Operation{
		OperationID: "getSettings", Method: http.MethodGet, Path: "/v1/settings",
		Summary: "Effective runtime settings", Tags: []string{"settings"},
		Description: "Returns every runtime setting and feature switch as it applies to the active organisation, with " +
			"its effective value, where it comes from (organisation → global → environment → default), the scope " +
			"it can be changed at and whether it is locked. Secrets are masked (`••••<last 4>`). Any member (`read`; " +
			"API keys need `settings:read`). Values are cached for up to 30 seconds per node. The `ETag` is the " +
			"settings version.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses:  map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: settingsExample}}}},
		Extensions: httpapi.WithScope(ext, "settings:read"),
	}, m.getSettings)

	httpapi.Register(srv, huma.Operation{
		OperationID: "patchSettings", Method: http.MethodPatch, Path: "/v1/settings",
		Summary: "Patch runtime settings", Tags: []string{"settings"},
		Description: "Changes runtime settings and feature switches at organisation scope (default; `settings.write`) or, " +
			"with `?scope=global`, instance-wide (instance owners only). The body is a partial object: either " +
			"`{\"values\": {...}, \"switches\": {...}}` or top-level keys (`\"webhookTimeout\": \"20s\"`, " +
			"`\"switches.sandbox.enabled\": true`). `null` removes an override so the next level applies. At global " +
			"scope a switch can be locked with `{\"value\": …, \"locked\": true}`. Settings that only the environment " +
			"can set give `422 setting_not_runtime`; locked ones `403 setting_locked`. `If-Match` guards against " +
			"concurrent edits. Running nodes pick the change up immediately (settings.changed notification). " +
			"Returns the new effective settings.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeSettingLocked},
			httpapi.ProblemDoc{Code: errs.CodeSettingNotRuntime, Detail: "This setting cannot be changed at runtime."},
			httpapi.ProblemDoc{Code: errs.CodeSwitchUnknown},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Invalid setting value."},
			httpapi.ProblemDoc{Code: errs.CodeVersionMismatch, Detail: "The settings version does not match If-Match."},
			httpapi.ProblemDoc{Code: errs.CodeConflict}),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
			"nested": {Summary: "values and switches objects", Value: map[string]any{
				"values":   map[string]any{"webhookTimeout": "20s", "authLockoutThreshold": 8},
				"switches": map[string]any{"sandbox.enabled": true, "protocolLogs.enabled": false},
			}},
			"flat":  {Summary: "Top-level keys", Value: map[string]any{"webhookTimeout": "20s", "switches.sandbox.enabled": true}},
			"reset": {Summary: "Remove an organisation override (null)", Value: map[string]any{"values": map[string]any{"webhookTimeout": nil}}},
			"lockGlobal": {Summary: "Lock a switch instance-wide (?scope=global)", Value: map[string]any{
				"switches": map[string]any{"sandbox.enabled": map[string]any{"value": false, "locked": true}},
			}},
		}}}},
		Responses:  map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: settingsExample}}}},
		Extensions: httpapi.WithScope(ext, "settings:write"),
	}, m.patchSettings)

	httpapi.Register(srv, huma.Operation{
		OperationID: "listFeatureSwitches", Method: http.MethodGet, Path: "/v1/feature-switches",
		Summary: "Feature-switch registry", Tags: []string{"settings"},
		Description: "Lists every optional feature switch EmailBridge knows (name, description, levels it can be set at, " +
			"default, lock state) with its effective value for the active organisation. Use it to build settings " +
			"screens; change values with `PATCH /v1/settings` (organisation, global) or on the account / endpoint " +
			"(`switches` field). Any member (`read`; API keys need `settings:read`). Not paginated.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Extensions: httpapi.WithScope(ext, "settings:read"),
	}, m.listSwitches)

	httpapi.Register(srv, huma.Operation{
		OperationID: "getMeSettings", Method: http.MethodGet, Path: "/v1/me/settings",
		Summary: "Per-user UI settings", Tags: []string{"me"},
		Description: "Returns the signed-in user's own UI preferences: table column visibility, order and widths per " +
			"`tableName`, theme / density / locale, notification preferences. Users only (API keys get `403`). An " +
			"empty object before the first save. The `ETag` is the version.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden),
		Extensions: httpapi.WithScope(ext, "me:read"),
	}, m.getMeSettings)

	httpapi.Register(srv, huma.Operation{
		OperationID: "patchMeSettings", Method: http.MethodPatch, Path: "/v1/me/settings",
		Summary: "Update per-user UI settings", Tags: []string{"me"},
		Description: "Deep-merges the body into the user's settings (JSON Merge Patch semantics: objects merge, arrays " +
			"and scalars replace, `null` deletes a key). Validated shapes: `tables.<name>.visibility` / `order` " +
			"(string arrays), `widths` (numbers), `ui.theme` / `density` / `locale` (supported locale), " +
			"`notifications.mutedCategories`, `notifications.email.{enabled,digest,minSeverity}` (`enabled` unset = " +
			"the role default: on for owners and admins, off for operators and viewers), `dashboard.period` " +
			"(`1h`, `24h`, `7d` or `30d`). At most 64 KiB in " +
			"total (`413`). Users only. `If-Match` is honoured. Returns the merged settings.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeValidationFailed,
			errs.CodePayloadTooLarge, errs.CodeVersionMismatch, errs.CodeConflict),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
			"table": {Summary: "Save a table's columns", Value: map[string]any{"tables": map[string]any{"accounts": map[string]any{
				"visibility": []any{"email", "state", "lastSyncAt"}, "order": []any{"email", "state", "lastSyncAt"}, "widths": map[string]any{"email": 280},
			}}}},
			"ui":     {Summary: "Theme and language", Value: map[string]any{"ui": map[string]any{"theme": "dark", "locale": "it"}}},
			"delete": {Summary: "Forget a table's settings (null deletes)", Value: map[string]any{"tables": map[string]any{"accounts": nil}}},
		}}}},
		Extensions: httpapi.WithScope(ext, "me:write"),
	}, m.patchMeSettings)
}

// settingsExample is a realistic GET /v1/settings body (documentation only).
var settingsExample = map[string]any{
	"values": map[string]any{
		"webhookTimeout":       map[string]any{"value": "20s", "source": "organization", "scope": "organization"},
		"authLockoutThreshold": map[string]any{"value": 5, "source": "default", "scope": "organization"},
		"sysmailSmtpPass":      map[string]any{"value": "••••x9Qa", "source": "global", "scope": "organization"},
	},
	"switches": map[string]any{
		"sandbox.enabled":      map[string]any{"value": false, "source": "global", "scope": "organization", "locked": true},
		"protocolLogs.enabled": map[string]any{"value": true, "source": "organization", "scope": "organization"},
	},
	"version": 7,
}

type settingsOut struct {
	ETag string `header:"ETag" doc:"Settings version; send as If-Match on PATCH." example:"\"v7\""`
	Body SettingsBody
}

type patchSettingsIn struct {
	Scope   string `query:"scope" doc:"organization (default) or global (instance owners)." example:"organization"`
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v7\""`
	Body    map[string]any
}

type switchesOut struct {
	Body struct {
		Data []FeatureSwitchBody `json:"data" doc:"Every feature switch."`
	}
}

type meSettingsOut struct {
	ETag string `header:"ETag" doc:"Version of the user's settings." example:"\"v4\""`
	Body UserSettingsBody
}

type patchMeIn struct {
	IfMatch string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v4\""`
	Body    map[string]any
}

func (m *Module) getSettings(ctx context.Context, _ *struct{}) (*settingsOut, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	body, err := m.svc.Effective(ctx, c.OrganizationID)
	if err != nil {
		return nil, err
	}
	return &settingsOut{ETag: formatETag(body.Version), Body: body}, nil
}

func (m *Module) patchSettings(ctx context.Context, in *patchSettingsIn) (*settingsOut, error) {
	scope := in.Scope
	if scope == "" {
		scope = "organization"
	}
	patch := in.Body
	if patch == nil {
		patch = map[string]any{}
	}
	body, err := m.svc.Patch(ctx, scope, patch, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &settingsOut{ETag: formatETag(body.Version), Body: body}, nil
}

func (m *Module) listSwitches(ctx context.Context, _ *struct{}) (*switchesOut, error) {
	data, err := m.svc.ListFeatureSwitches(ctx)
	if err != nil {
		return nil, err
	}
	out := &switchesOut{}
	out.Body.Data = data
	return out, nil
}

func (m *Module) getMeSettings(ctx context.Context, _ *struct{}) (*meSettingsOut, error) {
	body, err := m.svc.GetUserSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &meSettingsOut{ETag: formatETag(body.Version), Body: body}, nil
}

func (m *Module) patchMeSettings(ctx context.Context, in *patchMeIn) (*meSettingsOut, error) {
	patch := in.Body
	if patch == nil {
		patch = map[string]any{}
	}
	body, err := m.svc.PatchUserSettings(ctx, patch, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &meSettingsOut{ETag: formatETag(body.Version), Body: body}, nil
}
