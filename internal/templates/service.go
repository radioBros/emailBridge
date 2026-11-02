package templates

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchTemplates = "sending.templates"

// Service is the templates API and the render surface for submit.
type Service struct {
	Repo     *Repo
	Settings *settings.Service
	Registry *features.Registry
	Env      string
	Log      *slog.Logger
	Now      func() time.Time
	// MJML compiles htmlFormat mjml sources (G2); nil uses the process-wide compiler.
	MJML MJMLCompiler
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

// requireFeature refuses when sending.templates is off for the organisation.
func (s *Service) requireFeature(ctx context.Context) error {
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return store.ErrNoTenant
	}
	sw, ok := s.registry().Get(switchTemplates)
	if !ok {
		return nil
	}
	vals := features.Values{}
	if s.Settings != nil {
		if body, err := s.Settings.Effective(ctx, org); err == nil {
			if sv, exists := body.Switches[switchTemplates]; exists {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	} else if s.Repo != nil && s.Repo.DB != nil {
		var doc settings.Doc
		_ = s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc)
		if doc.Switches != nil {
			if sv, ok := doc.Switches[switchTemplates]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil && !*v.Bool {
		return errs.New(errs.CodeFeatureDisabled, "sending.templates is disabled for this organisation.")
	}
	return nil
}

// Create inserts a template.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Template, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	if err := s.requireFeature(ctx); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	name, err := validateName(in.Name)
	if err != nil {
		return nil, err
	}
	format, err := normalizeFormat(in.HTMLFormat)
	if err != nil {
		return nil, err
	}
	if err := validateBodies(in.Subject, in.Text, in.HTML, in.Partials); err != nil {
		return nil, err
	}
	cnt := content{Subject: in.Subject, Text: in.Text, HTML: in.HTML, Partials: in.Partials,
		Strict: in.Strict, Locale: in.Locale, Timezone: in.Timezone}
	if err := ValidateSources(cnt); err != nil {
		return nil, err
	}
	compiled, err := s.compileHTML(ctx, format, in.HTML)
	if err != nil {
		return nil, err
	}
	now := s.now()
	t := &Template{
		ID: store.NewID(store.PrefixTemplate), OrganizationID: c.OrganizationID, Name: name,
		Engine: EngineHandlebars, Subject: in.Subject, Text: in.Text, HTML: in.HTML,
		HTMLFormat: format, HTMLCompiled: compiled,
		Partials: cloneMap(in.Partials), Strict: in.Strict, Locale: defaultStr(in.Locale, "en"),
		Timezone: defaultStr(in.Timezone, "UTC"), CurrentVersion: 1, CreatedBy: c.Actor(),
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := s.Repo.insert(ctx, t, snapshot(t, c.Actor(), now)); err != nil {
		return nil, err
	}
	return t, nil
}

// Get returns one template.
func (s *Service) Get(ctx context.Context, id string) (*Template, error) {
	if err := tenancy.RequireRead(ctx, "templates"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixTemplate, id) {
		return nil, errs.New(errs.CodeTemplateNotFound, "The template does not exist.").WithSafe("templateId", id)
	}
	return s.Repo.get(ctx, id)
}

// Update patches a template.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput, ifMatch string) (*Template, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	if err := s.requireFeature(ctx); err != nil {
		return nil, err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	oldVersion := cur.Version
	contentChanged := false
	recompile := false
	if in.Name != nil {
		name, err := validateName(*in.Name)
		if err != nil {
			return nil, err
		}
		cur.Name = name
	}
	if in.Subject != nil {
		cur.Subject = *in.Subject
		contentChanged = true
	}
	if in.Text != nil {
		cur.Text = *in.Text
		contentChanged = true
	}
	if in.HTML != nil {
		cur.HTML = *in.HTML
		contentChanged = true
		recompile = true
	}
	if in.HTMLFormat != nil {
		format, err := normalizeFormat(*in.HTMLFormat)
		if err != nil {
			return nil, err
		}
		cur.HTMLFormat = format
		contentChanged = true
		recompile = true
	}
	if in.Partials != nil {
		cur.Partials = cloneMap(*in.Partials)
		contentChanged = true
	}
	if in.Strict != nil {
		cur.Strict = *in.Strict
		contentChanged = true
	}
	if in.Locale != nil {
		cur.Locale = defaultStr(*in.Locale, "en")
		contentChanged = true
	}
	if in.Timezone != nil {
		cur.Timezone = defaultStr(*in.Timezone, "UTC")
		contentChanged = true
	}
	if err := validateBodies(cur.Subject, cur.Text, cur.HTML, cur.Partials); err != nil {
		return nil, err
	}
	if err := ValidateSources(sourcesOf(cur)); err != nil {
		return nil, err
	}
	if recompile {
		compiled, err := s.compileHTML(ctx, formatOf(cur.HTMLFormat), cur.HTML)
		if err != nil {
			return nil, err
		}
		cur.HTMLCompiled = compiled
	}
	now := s.now()
	cur.UpdatedAt = now
	cur.Version = oldVersion + 1
	var ver *Version
	if contentChanged {
		cur.CurrentVersion++
		ver = snapshot(cur, c.Actor(), now)
	}
	if err := s.Repo.replace(ctx, cur, oldVersion, ver); err != nil {
		return nil, err
	}
	return cur, nil
}

// Delete removes a template.
func (s *Service) Delete(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return err
	}
	if !store.IsID(store.PrefixTemplate, id) {
		return errs.New(errs.CodeTemplateNotFound, "The template does not exist.").WithSafe("templateId", id)
	}
	var ver int64
	if ifMatch != "" {
		cur, err := s.Repo.get(ctx, id)
		if err != nil {
			return err
		}
		if parseETag(ifMatch) != cur.Version {
			return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
		}
		ver = cur.Version
	}
	return s.Repo.delete(ctx, id, ver)
}

// Preview renders a template with sample variables.
func (s *Service) Preview(ctx context.Context, id string, in PreviewInput) (*PreviewResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	if err := s.requireFeature(ctx); err != nil {
		return nil, err
	}
	cnt, err := s.loadContent(ctx, id, in.Version)
	if err != nil {
		return nil, err
	}
	if in.Locale != "" {
		cnt.Locale = in.Locale
	}
	if in.Timezone != "" {
		cnt.Timezone = in.Timezone
	}
	out, err := Render(cnt, in.Variables)
	if err != nil {
		return nil, err
	}
	return &PreviewResult{Subject: out.Subject, Text: out.Text, HTML: out.HTML}, nil
}

// RenderForSubmit loads and renders a template for outbound submit.
func (s *Service) RenderForSubmit(ctx context.Context, id string, version *int, vars map[string]any) (Rendered, error) {
	if err := s.requireFeature(ctx); err != nil {
		return Rendered{}, err
	}
	cnt, err := s.loadContent(ctx, id, version)
	if err != nil {
		return Rendered{}, err
	}
	return Render(cnt, vars)
}

func (s *Service) loadContent(ctx context.Context, id string, version *int) (content, error) {
	if !store.IsID(store.PrefixTemplate, id) {
		return content{}, errs.New(errs.CodeTemplateNotFound, "The template does not exist.").WithSafe("templateId", id)
	}
	t, err := s.Repo.get(ctx, id)
	if err != nil {
		return content{}, err
	}
	if version == nil || *version == 0 || *version == t.CurrentVersion {
		return contentOf(t), nil
	}
	v, err := s.Repo.getVersion(ctx, id, *version)
	if err != nil {
		return content{}, err
	}
	return contentOfVersion(v), nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return "", errs.New(errs.CodeValidationFailed, "name must be 1 to 128 characters.")
	}
	return name, nil
}

func validateBodies(subject, text, html string, partials map[string]string) error {
	if subject == "" && text == "" && html == "" {
		return errs.New(errs.CodeValidationFailed, "A template needs subject, text or html.")
	}
	if utf8.RuneCountInString(subject) > MaxSubjectSource {
		return errs.New(errs.CodeValidationFailed, "subject source is too long.")
	}
	if len(text) > MaxBodySource || len(html) > MaxBodySource {
		return errs.New(errs.CodeValidationFailed, "text/html source exceeds 256 KiB.")
	}
	if len(partials) > MaxPartials {
		return errs.New(errs.CodeValidationFailed, "Too many partials.").WithSafe("limit", MaxPartials)
	}
	for name, src := range partials {
		if name == "" || utf8.RuneCountInString(name) > MaxPartialName {
			return errs.New(errs.CodeValidationFailed, "Invalid partial name.")
		}
		if len(src) > MaxPartialSource {
			return errs.New(errs.CodeValidationFailed, "Partial source exceeds 64 KiB.").WithSafe("partial", name)
		}
	}
	return nil
}

func defaultStr(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func parseETag(v string) int64 {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"`)
	v = strings.TrimPrefix(v, "v")
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func formatETag(v int64) string { return `"` + "v" + strconv.FormatInt(v, 10) + `"` }
