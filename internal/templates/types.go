// Package templates owns organisation send templates (Handlebars), preview and the
// render surface used by submit for templated sends and mail merge (P5.1).
package templates

import "time"

// Collections (S1 §6, P5.1).
const (
	Collection         = "templates"
	VersionsCollection = "templateVersions"
)

// EngineHandlebars is the only supported engine.
const EngineHandlebars = "handlebars"

// HTML part formats (G2): the html source is HTML, or MJML compiled to HTML when the template is saved.
const (
	HTMLFormatHTML = "html"
	HTMLFormatMJML = "mjml"
)

// Size / count caps (P5.1 §1).
const (
	MaxNameLen       = 128
	MaxSubjectSource = 998
	MaxBodySource    = 256 << 10
	MaxPartials      = 32
	MaxPartialName   = 64
	MaxPartialSource = 64 << 10
	// MaxCompiledHTML caps the HTML an MJML source compiles to (MJML expands its source several times).
	MaxCompiledHTML = 1 << 20
)

// Template is the stored and API form of one send template.
type Template struct {
	ID             string            `bson:"_id" json:"id" doc:"Template ID (tpl_…)." example:"tpl_01m5djyy00ervzha8qtwwqsg2q"`
	OrganizationID string            `bson:"organizationId" json:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string            `bson:"name" json:"name" doc:"Unique name in the organisation (1–128 characters, case-sensitive)." example:"Invoice ready"`
	Engine         string            `bson:"engine" json:"engine" doc:"Template engine; always handlebars." example:"handlebars"`
	Subject        string            `bson:"subject,omitempty" json:"subject,omitempty" doc:"Handlebars source of the subject (≤ 998 characters; not HTML-escaped)." example:"Invoice {{invoice.number}} for {{date invoice.issued format=\"January 2006\"}}"`
	Text           string            `bson:"text,omitempty" json:"text,omitempty" doc:"Handlebars source of the plain-text body (≤ 256 KiB; not HTML-escaped)." example:"Hello {{default customer.firstName \"there\"}},\n\nyour invoice {{invoice.number}} of {{invoice.total}} is ready.\n\n{{> signature}}"`
	HTML           string            `bson:"html,omitempty" json:"html,omitempty" doc:"Handlebars source of the HTML body (≤ 256 KiB; {{var}} is HTML-escaped, {{{var}}} is not). With htmlFormat mjml it is the MJML source." example:"<p>Hello {{default customer.firstName \"there\"}},</p><p>your invoice <b>{{invoice.number}}</b> is ready.</p>{{> signature}}"`
	HTMLFormat     string            `bson:"htmlFormat,omitempty" json:"htmlFormat" enum:"html,mjml" doc:"Format of html: html (default) or mjml (compiled to HTML when the template is saved; Handlebars runs on the compiled HTML at preview and send)." example:"html"`
	HTMLCompiled   string            `bson:"htmlCompiled,omitempty" json:"htmlCompiled,omitempty" doc:"Read-only: the HTML the MJML source compiled to, Handlebars placeholders still in place (mjml templates on GET, create and update; omitted from lists)." example:"<!doctype html><html>…<div>Hello {{default customer.firstName \"there\"}},</div>…</html>"`
	Partials       map[string]string `bson:"partials,omitempty" json:"partials,omitempty" doc:"Partials by name ({{> name}}), at most 32, names ≤ 64 characters, sources ≤ 64 KiB. They are inserted after MJML compilation, so they are always HTML / text, never MJML." example:"{\"signature\":\"Kind regards\\nBilling team, Example Ltd\"}"`
	Strict         bool              `bson:"strict,omitempty" json:"strict,omitempty" doc:"true: a missing variable fails the render (422 template_variable_missing) instead of rendering empty." example:"true"`
	Locale         string            `bson:"locale,omitempty" json:"locale,omitempty" doc:"Default locale of the date helper (default en)." example:"en"`
	Timezone       string            `bson:"timezone,omitempty" json:"timezone,omitempty" doc:"Default IANA time zone of the date helper (default UTC)." example:"Europe/Rome"`
	CurrentVersion int               `bson:"currentVersion" json:"currentVersion" doc:"Content version: 1 on create, +1 on every content change." example:"1"`
	CreatedBy      string            `bson:"createdBy,omitempty" json:"createdBy,omitempty" doc:"User or API key that created the template." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt      time.Time         `bson:"createdAt" json:"createdAt" doc:"When the template was created." example:"2026-10-27T11:02:00Z"`
	UpdatedAt      time.Time         `bson:"updatedAt" json:"updatedAt" doc:"Last change." example:"2026-10-27T11:02:00Z"`
	Version        int64             `bson:"version" json:"version" doc:"Optimistic-concurrency version (the ETag is \"v<version>\"); +1 on every change." example:"1"`
}

// Version is one immutable content snapshot.
type Version struct {
	ID             string            `bson:"_id" json:"id"`
	OrganizationID string            `bson:"organizationId" json:"organizationId"`
	TemplateID     string            `bson:"templateId" json:"templateId"`
	Version        int               `bson:"version" json:"version"`
	Subject        string            `bson:"subject,omitempty" json:"subject,omitempty"`
	Text           string            `bson:"text,omitempty" json:"text,omitempty"`
	HTML           string            `bson:"html,omitempty" json:"html,omitempty"`
	HTMLFormat     string            `bson:"htmlFormat,omitempty" json:"htmlFormat,omitempty"`
	HTMLCompiled   string            `bson:"htmlCompiled,omitempty" json:"htmlCompiled,omitempty"`
	Partials       map[string]string `bson:"partials,omitempty" json:"partials,omitempty"`
	Strict         bool              `bson:"strict,omitempty" json:"strict,omitempty"`
	Locale         string            `bson:"locale,omitempty" json:"locale,omitempty"`
	Timezone       string            `bson:"timezone,omitempty" json:"timezone,omitempty"`
	CreatedBy      string            `bson:"createdBy,omitempty" json:"createdBy,omitempty"`
	CreatedAt      time.Time         `bson:"createdAt" json:"createdAt"`
}

// CreateInput is POST /v1/templates.
type CreateInput struct {
	Name       string            `json:"name" doc:"Unique name in the organisation, 1–128 characters (required)." example:"Invoice ready"`
	Subject    string            `json:"subject,omitempty" doc:"Handlebars source of the subject (≤ 998 characters)." example:"Invoice {{invoice.number}} for {{date invoice.issued format=\"January 2006\"}}"`
	Text       string            `json:"text,omitempty" doc:"Handlebars source of the plain-text body (≤ 256 KiB)." example:"Hello {{default customer.firstName \"there\"}},\n\nyour invoice {{invoice.number}} of {{invoice.total}} is ready.\n\n{{> signature}}"`
	HTML       string            `json:"html,omitempty" doc:"Handlebars source of the HTML body (≤ 256 KiB), or its MJML source with htmlFormat mjml. At least one of subject, text and html is required." example:"<p>Hello {{default customer.firstName \"there\"}},</p><p>your invoice <b>{{invoice.number}}</b> is ready.</p>{{> signature}}"`
	HTMLFormat string            `json:"htmlFormat,omitempty" enum:"html,mjml" doc:"Format of html: html (default) or mjml. Invalid MJML is 422 validation_failed with one errors[] item per problem at body.html." example:"html"`
	Partials   map[string]string `json:"partials,omitempty" doc:"Partials by name ({{> name}}), at most 32." example:"{\"signature\":\"Kind regards\\nBilling team, Example Ltd\"}"`
	Strict     bool              `json:"strict,omitempty" doc:"Fail the render when a variable is missing (default false)." example:"true"`
	Locale     string            `json:"locale,omitempty" doc:"Default locale of the date helper (default en)." example:"en"`
	Timezone   string            `json:"timezone,omitempty" doc:"Default IANA time zone of the date helper (default UTC)." example:"Europe/Rome"`
}

// UpdateInput is PATCH /v1/templates/{templateId} (JSON Merge Patch).
type UpdateInput struct {
	Name       *string            `json:"name,omitempty" doc:"New unique name." example:"Invoice ready (2026)"`
	Subject    *string            `json:"subject,omitempty" doc:"New subject source." example:"Your invoice {{invoice.number}} is ready"`
	Text       *string            `json:"text,omitempty" doc:"New plain-text source." example:"Hello {{default customer.firstName \"there\"}},\n\nyour invoice {{invoice.number}} of {{invoice.total}} is ready.\n\n{{> signature}}"`
	HTML       *string            `json:"html,omitempty" doc:"New HTML source (MJML source when htmlFormat is mjml)." example:"<p>Hello {{default customer.firstName \"there\"}},</p><p>your invoice <b>{{invoice.number}}</b> is ready.</p>{{> signature}}"`
	HTMLFormat *string            `json:"htmlFormat,omitempty" enum:"html,mjml" doc:"New format of html: html or mjml. A change recompiles html (or drops htmlCompiled when switching to html) and is a content change." example:"mjml"`
	Partials   *map[string]string `json:"partials,omitempty" doc:"Replaces all partials." example:"{\"signature\":\"Kind regards\\nBilling team, Example Ltd\"}"`
	Strict     *bool              `json:"strict,omitempty" doc:"Fail the render when a variable is missing." example:"false"`
	Locale     *string            `json:"locale,omitempty" doc:"Default locale of the date helper (empty: en)." example:"it"`
	Timezone   *string            `json:"timezone,omitempty" doc:"Default IANA time zone of the date helper (empty: UTC)." example:"Europe/Rome"`
}

// PreviewInput is POST /v1/templates/{templateId}/preview.
type PreviewInput struct {
	Version   *int           `json:"version,omitempty" doc:"Content version to render (default: currentVersion)." example:"1"`
	Variables map[string]any `json:"variables,omitempty" doc:"Sample variables, as a send would pass them." example:"{\"customer\":{\"firstName\":\"Anna\"},\"invoice\":{\"number\":\"2026-1043\",\"total\":\"€ 1,250.00\",\"issued\":\"2026-10-27T09:00:00Z\"}}"`
	Locale    string         `json:"locale,omitempty" doc:"Override the template's locale for this render." example:"en"`
	Timezone  string         `json:"timezone,omitempty" doc:"Override the template's time zone for this render." example:"Europe/Rome"`
}

// PreviewResult is the preview response.
type PreviewResult struct {
	Subject string `json:"subject,omitempty" doc:"Rendered subject." example:"Invoice 2026-1043 for October 2026"`
	Text    string `json:"text,omitempty" doc:"Rendered plain-text body." example:"Hello Anna,\n\nyour invoice 2026-1043 of € 1,250.00 is ready.\n\nKind regards\nBilling team, Example Ltd"`
	HTML    string `json:"html,omitempty" doc:"Rendered HTML body." example:"<p>Hello Anna,</p><p>your invoice <b>2026-1043</b> is ready.</p>Kind regards\nBilling team, Example Ltd"`
}

// ListFilter holds GET /v1/templates query parameters.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Q            string
}

// List is a cursor/page list response.
type List struct {
	Data        []Template `json:"data" doc:"The page of templates."`
	NextCursor  *string    `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6InRwbF8wMW01ZGd2NjEwcjNjOGIxbjZtMng5ejRxdyIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64     `json:"total,omitempty" doc:"Number of matching templates, with page and includeTotal=true (at most 10 000)." example:"2"`
	TotalCapped *bool      `json:"totalCapped,omitempty" doc:"true when total stopped at 10 000." example:"false"`
}

// Rendered is the output of rendering a template for submit.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}
