package templates

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(srv *httpapi.Server) {
	ext := httpapi.Phase("P5")
	const readPerm = "Requires the `read` permission (every role; API keys `templates:read`). "
	const writePerm = "Requires `outbound.write` (owner, admin, operator; API keys `outbound:write`) and the " +
		"`sending.templates` switch (`403 feature_disabled` when off). "
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the scope this operation needs."}}
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, auth...), extra...)...)
	}
	disabled := httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "sending.templates is disabled for this organisation."}
	notFound := httpapi.ProblemDoc{Code: errs.CodeTemplateNotFound, Detail: "The template does not exist."}
	nameTaken := httpapi.ProblemDoc{Code: errs.CodeTemplateNameTaken, Detail: "A template with this name already exists."}
	invalid := httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Invalid Handlebars in html: Parse error on line 3: Expecting 'ID', got 'CLOSE'."}
	invalidMJML := httpapi.ProblemDoc{Code: errs.CodeValidationFailed,
		Detail: "Invalid MJML in html: Line 6, mj-bogus: Element mj-bogus doesn't exist or is not registered",
		Errors: []errs.ProblemItem{
			{Path: "body.html", Code: "invalid", Message: "Line 6, mj-bogus: Element mj-bogus doesn't exist or is not registered"},
			{Path: "body.html", Code: "invalid", Message: "Line 7, mj-image: Attribute width has invalid value: abc for type Unit, only accepts (px) units and 1 value(s)"},
		}}
	const mjmlDoc = "With `htmlFormat: \"mjml\"` the `html` part is MJML: it is compiled to HTML once, when the template " +
		"is saved (strict validation, at most 10 s, compiled HTML ≤ 1 MiB, no `<mj-include>`, no automatic web-font " +
		"links), and Handlebars runs on the compiled HTML at preview and send, so `{{…}}` works in text and in " +
		"attributes such as `href`. Invalid MJML is `422 validation_failed` with one `errors[]` item per problem at " +
		"`body.html`. The compiled HTML is returned read-only as `htmlCompiled`. "
	httpapi.Register(srv, huma.Operation{
		OperationID: "listTemplates", Method: http.MethodGet, Path: "/v1/templates",
		Summary: "List templates", Tags: []string{"templates"},
		Description: "Lists the organisation's send templates (full sources included), newest first by default " +
			"(`-id`; `sort` also `name`, `createdAt`). `q` matches part of the name (case-insensitive, ≤ 200 " +
			"characters). Paged by `cursor` or `page` (+ `includeTotal`, capped at 10 000 with `totalCapped`); " +
			"`limit` 1–200 (default 50). " + readPerm + "Read-only.",
		Metadata: problems(httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "q must be at most 200 characters."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleTemplateList())}},
		Extensions: httpapi.WithScope(ext, "templates:read"),
	}, m.listTemplates)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createTemplate", Method: http.MethodPost, Path: "/v1/templates",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create template", Tags: []string{"templates"},
		Description: "Creates a Handlebars send template: `subject`, `text` and / or `html` (at least one), optional " +
			"`partials` (`{{> name}}`), `strict` (a missing variable fails the render with `422 " +
			"template_variable_missing`) and the `locale` / `timezone` defaults of the `date` helper. Every part and " +
			"partial must parse (`422 validation_failed`). " + mjmlDoc + "Helpers: `date`, `default`, `upper`, `lower`, `url`; HTML " +
			"output is escaped (`{{{raw}}}` for trusted HTML). Names are unique per organisation (`409 " +
			"template_name_taken`). Send with `template: { id, variables }` on `POST …/outbound-messages`. " + writePerm +
			"Returns `Location` and `ETag`.",
		Metadata: problems(disabled, invalid, invalidMJML, nameTaken, httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"html": {Summary: "HTML part", Value: map[string]any{
				"name":     "Invoice ready",
				"subject":  "Invoice {{invoice.number}} for {{date invoice.issued format=\"January 2006\"}}",
				"text":     "Hello {{default customer.firstName \"there\"}},\n\nyour invoice {{invoice.number}} of {{invoice.total}} is ready.\n\n{{> signature}}",
				"html":     "<p>Hello {{default customer.firstName \"there\"}},</p><p>your invoice <b>{{invoice.number}}</b> of {{invoice.total}} is ready: <a href=\"https://billing.example.com/i/{{url invoice.number}}\">view it</a>.</p>{{> signature}}",
				"partials": map[string]any{"signature": "Kind regards\nBilling team, Example Ltd"},
				"strict":   true, "locale": "en", "timezone": "Europe/Rome",
			}},
			"mjml": {Summary: "MJML part", Value: map[string]any{
				"name":       "Invoice ready (responsive)",
				"subject":    "Invoice {{invoice.number}} is ready",
				"htmlFormat": "mjml",
				"html":       exampleMJML,
			}},
		})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonExample(exampleTemplate())}},
		Extensions: httpapi.WithScope(ext, "outbound:write"),
	}, m.createTemplate)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getTemplate", Method: http.MethodGet, Path: "/v1/templates/{templateId}",
		Summary: "Get template", Tags: []string{"templates"},
		Description: "Returns one template with its sources, `htmlFormat` (and, for MJML templates, the read-only " +
			"`htmlCompiled`), `currentVersion` (content version, used by `version` in " +
			"preview and submit) and its `ETag` (send it as `If-Match` on PATCH / DELETE). " + readPerm + "Read-only.",
		Metadata:   problems(notFound),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleTemplate())}},
		Extensions: httpapi.WithScope(ext, "templates:read"),
	}, m.getTemplate)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchTemplate", Method: http.MethodPatch, Path: "/v1/templates/{templateId}",
		Summary: "Update template", Tags: []string{"templates"},
		Description: "Changes a template (JSON Merge Patch: omitted fields stay). A change to any content field " +
			"(subject, text, html, htmlFormat, partials, strict, locale, timezone) stores the previous content as an immutable " +
			"version and increments `currentVersion`; renaming alone does not. Queued messages rendered from an older " +
			"version are not touched. A change to `html` or `htmlFormat` recompiles an MJML part (switching to " +
			"`html` drops `htmlCompiled`); invalid MJML is `422 validation_failed` with `errors[]` at `body.html`. " +
			"Send the last `ETag` as `If-Match` (`412 version_mismatch`). " + writePerm,
		Metadata: problems(disabled, notFound, invalid, invalidMJML, nameTaken, httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"subject": {Summary: "Change the subject", Value: map[string]any{"subject": "Your invoice {{invoice.number}} is ready"}},
			"rename":  {Summary: "Rename only", Value: map[string]any{"name": "Invoice ready (2026)"}},
			"mjml":    {Summary: "Switch the HTML part to MJML", Value: map[string]any{"htmlFormat": "mjml", "html": exampleMJML}},
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			t := exampleTemplate()
			t["subject"] = "Your invoice {{invoice.number}} is ready"
			t["currentVersion"] = 2
			t["version"] = 2
			t["updatedAt"] = "2026-10-27T11:20:00Z"
			return t
		}())}},
		Extensions: httpapi.WithScope(ext, "outbound:write"),
	}, m.patchTemplate)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteTemplate", Method: http.MethodDelete, Path: "/v1/templates/{templateId}",
		Summary: "Delete template", Tags: []string{"templates"},
		Description: "Deletes the template and answers `204`; messages already queued from it keep their rendered " +
			"content, new sends naming it fail with `404 template_not_found`. `If-Match` is optional. Requires " +
			"`outbound.write` (owner, admin, operator; API keys `outbound:write`). Not reversible.",
		Metadata:   problems(notFound, httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(ext, "outbound:write"),
	}, m.deleteTemplate)
	httpapi.Register(srv, huma.Operation{
		OperationID: "previewTemplate", Method: http.MethodPost, Path: "/v1/templates/{templateId}/preview",
		Summary: "Preview template", Tags: []string{"templates"},
		Description: "Renders the template (the current content, or an older `version`) with sample `variables` and " +
			"returns subject, text and HTML exactly as a send would produce them (an MJML part renders from the HTML " +
			"compiled when that version was saved); `locale` / `timezone` override the " +
			"template's defaults. Nothing is stored or sent. A missing variable in a `strict` template is `422 " +
			"template_variable_missing`; any other render error `422 template_render_failed`. " + writePerm,
		Metadata: problems(disabled, notFound,
			httpapi.ProblemDoc{Code: errs.CodeTemplateVariableMissing, Detail: "A required template variable is missing."},
			httpapi.ProblemDoc{Code: errs.CodeTemplateRenderFailed, Detail: "The template could not be rendered."}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{
			"variables": map[string]any{
				"customer": map[string]any{"firstName": "Anna"},
				"invoice":  map[string]any{"number": "2026-1043", "total": "€ 1,250.00", "issued": "2026-10-27T09:00:00Z"},
			},
			"locale": "en", "timezone": "Europe/Rome",
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(map[string]any{
			"subject": "Invoice 2026-1043 for October 2026",
			"text":    "Hello Anna,\n\nyour invoice 2026-1043 of € 1,250.00 is ready.\n\nKind regards\nBilling team, Example Ltd",
			"html":    "<p>Hello Anna,</p><p>your invoice <b>2026-1043</b> of € 1,250.00 is ready: <a href=\"https://billing.example.com/i/2026-1043\">view it</a>.</p>Kind regards\nBilling team, Example Ltd",
		})}},
		Extensions: httpapi.WithScope(ext, "outbound:write"),
	}, m.previewTemplate)
}

const exampleMJML = "<mjml><mj-body><mj-section><mj-column>" +
	"<mj-text>Hello {{default customer.firstName \"there\"}}, your invoice {{invoice.number}} is ready.</mj-text>" +
	"<mj-button href=\"https://billing.example.com/i/{{url invoice.number}}\">View invoice</mj-button>" +
	"</mj-column></mj-section></mj-body></mjml>"

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleTemplate() map[string]any {
	return map[string]any{
		"id": "tpl_01m5djyy00ervzha8qtwwqsg2q", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Invoice ready", "engine": "handlebars",
		"subject":    "Invoice {{invoice.number}} for {{date invoice.issued format=\"January 2006\"}}",
		"text":       "Hello {{default customer.firstName \"there\"}},\n\nyour invoice {{invoice.number}} of {{invoice.total}} is ready.\n\n{{> signature}}",
		"html":       "<p>Hello {{default customer.firstName \"there\"}},</p><p>your invoice <b>{{invoice.number}}</b> of {{invoice.total}} is ready: <a href=\"https://billing.example.com/i/{{url invoice.number}}\">view it</a>.</p>{{> signature}}",
		"partials":   map[string]any{"signature": "Kind regards\nBilling team, Example Ltd"},
		"htmlFormat": "html",
		"strict":     true, "locale": "en", "timezone": "Europe/Rome", "currentVersion": 1,
		"createdBy": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-27T11:02:00Z",
		"updatedAt": "2026-10-27T11:02:00Z", "version": 1,
	}
}

func exampleTemplateList() map[string]any {
	second := map[string]any{
		"id": "tpl_01m5dgv610r3c8b1n6m2x9z4qw", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Password reset", "engine": "handlebars",
		"subject": "Your password reset code", "text": "Your code is {{code}}. It expires in 15 minutes.",
		"htmlFormat": "html",
		"locale":     "en", "timezone": "UTC", "currentVersion": 3,
		"createdBy": "key_01m5cqe6d0rs9cyxvy69ynf0t6", "createdAt": "2026-10-21T08:30:00Z",
		"updatedAt": "2026-10-25T16:12:44Z", "version": 4,
	}
	return map[string]any{
		"data":       []any{exampleTemplate(), second},
		"nextCursor": "eyJpZCI6InRwbF8wMW01ZGd2NjEwcjNjOGIxbjZtMng5ejRxdyIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size 1–200 (default 50)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6InRwbF8wMW01ZGd2NjEwcjNjOGIxbjZtMng5ejRxdyIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000, then totalCapped is true)." example:"true"`
	Sort         string `query:"sort" doc:"name, createdAt or id; prefix - for descending. Default -id (newest first)." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive part of the template name (≤ 200 characters)." example:"invoice"`
}

type listOut struct {
	Body List
}

type createIn struct {
	Body CreateInput
}

type templateOut struct {
	ETag     string `header:"ETag" doc:"Version of the new template; send it as If-Match on the next write." example:"\"v1\""`
	Location string `header:"Location" doc:"URL of the new template." example:"/v1/templates/tpl_01m5djyy00ervzha8qtwwqsg2q"`
	Status   int
	Body     Template
}

type templateIDIn struct {
	TemplateID string `path:"templateId" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
}

type getOut struct {
	ETag string `header:"ETag" doc:"Version of the template; send it as If-Match on the next write." example:"\"v2\""`
	Body Template
}

type patchIn struct {
	TemplateID string `path:"templateId" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
	IfMatch    string `header:"If-Match"`
	Body       UpdateInput
}

type deleteIn struct {
	TemplateID string `path:"templateId" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
	IfMatch    string `header:"If-Match"`
}

type previewIn struct {
	TemplateID string `path:"templateId" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
	Body       PreviewInput
}

type previewOut struct {
	Body PreviewResult
}

func (m *Module) listTemplates(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) createTemplate(ctx context.Context, in *createIn) (*templateOut, error) {
	t, err := m.svc.Create(ctx, in.Body)
	if err != nil {
		return nil, problemOf(ctx, err)
	}
	return &templateOut{
		Status: http.StatusCreated, ETag: formatETag(t.Version),
		Location: "/v1/templates/" + t.ID, Body: *t,
	}, nil
}

func (m *Module) getTemplate(ctx context.Context, in *templateIDIn) (*getOut, error) {
	t, err := m.svc.Get(ctx, in.TemplateID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(t.Version), Body: *t}, nil
}

func (m *Module) patchTemplate(ctx context.Context, in *patchIn) (*getOut, error) {
	t, err := m.svc.Update(ctx, in.TemplateID, in.Body, in.IfMatch)
	if err != nil {
		return nil, problemOf(ctx, err)
	}
	return &getOut{ETag: formatETag(t.Version), Body: *t}, nil
}

func (m *Module) deleteTemplate(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.svc.Delete(ctx, in.TemplateID, in.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) previewTemplate(ctx context.Context, in *previewIn) (*previewOut, error) {
	res, err := m.svc.Preview(ctx, in.TemplateID, in.Body)
	if err != nil {
		return nil, err
	}
	return &previewOut{Body: *res}, nil
}
