package templates

import (
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	hbs "github.com/radiobros/emailbridge/internal/templates/hbs"
)

// content is the renderable parts of a template or version.
type content struct {
	Subject  string
	Text     string
	HTML     string
	Partials map[string]string
	Strict   bool
	Locale   string
	Timezone string
}

// Render evaluates subject/text/html with variables.
func Render(c content, vars map[string]any) (Rendered, error) {
	out := Rendered{}
	var err error
	if c.Subject != "" {
		out.Subject, err = renderPart(c.Subject, vars, c, true)
		if err != nil {
			return Rendered{}, err
		}
	}
	if c.Text != "" {
		out.Text, err = renderPart(c.Text, vars, c, true)
		if err != nil {
			return Rendered{}, err
		}
	}
	if c.HTML != "" {
		out.HTML, err = renderPart(c.HTML, vars, c, false)
		if err != nil {
			return Rendered{}, err
		}
	}
	return out, nil
}

func renderPart(source string, vars map[string]any, c content, noEscape bool) (string, error) {
	tpl, err := hbs.Parse(source)
	if err != nil {
		return "", mapRenderErr(err)
	}
	tpl.NoEscape = noEscape
	tpl.Strict = c.Strict
	registerBuiltinHelpers(tpl, c.Locale, c.Timezone)
	for name, src := range c.Partials {
		tpl.RegisterPartial(name, src)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	out, err := tpl.Exec(vars)
	if err != nil {
		return "", mapRenderErr(err)
	}
	return out, nil
}

// ValidateSources parses every part and partial without executing.
func ValidateSources(c content) error {
	check := func(label, src string) error {
		if src == "" {
			return nil
		}
		if _, err := hbs.Parse(src); err != nil {
			return errs.New(errs.CodeValidationFailed, "Invalid Handlebars in "+label+": "+err.Error()).
				WithSafe("part", label)
		}
		return nil
	}
	if err := check("subject", c.Subject); err != nil {
		return err
	}
	if err := check("text", c.Text); err != nil {
		return err
	}
	if err := check("html", c.HTML); err != nil {
		return err
	}
	for name, src := range c.Partials {
		if err := check("partial:"+name, src); err != nil {
			return err
		}
	}
	return nil
}

func mapRenderErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "Missing required variable:") {
		name := strings.TrimSpace(strings.TrimPrefix(msg, "Missing required variable:"))
		return errs.New(errs.CodeTemplateVariableMissing, "A required template variable is missing.").
			WithSafe("variable", name)
	}
	return errs.New(errs.CodeTemplateRenderFailed, "The template could not be rendered.").
		WithSafe("detail", truncate(msg, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
