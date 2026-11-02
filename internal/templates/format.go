package templates

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	hbs "github.com/radiobros/emailbridge/internal/templates/hbs"
	"github.com/radiobros/emailbridge/internal/templates/mjml"
)

// MJMLCompiler turns MJML into HTML (G2). *mjml.Compiler implements it; tests use fakes.
type MJMLCompiler interface {
	Compile(ctx context.Context, src string) (string, error)
}

// htmlField is the problem path of the HTML part.
const htmlField = "body.html"

// mjInclude matches <mj-include>: the server compiler has no file system, so includes are refused instead
// of silently dropped.
var mjInclude = regexp.MustCompile(`(?i)<mj-include\b`)

func (s *Service) mjml() MJMLCompiler {
	if s.MJML != nil {
		return s.MJML
	}
	return mjml.Default()
}

// normalizeFormat validates an htmlFormat value; empty means html.
func normalizeFormat(f string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "", HTMLFormatHTML:
		return HTMLFormatHTML, nil
	case HTMLFormatMJML:
		return HTMLFormatMJML, nil
	}
	return "", fieldProblem("htmlFormat must be html or mjml.", "body.htmlFormat", "enum",
		[]string{"htmlFormat must be html or mjml."})
}

// formatOf is the effective format of a stored template (documents written before G2 have none).
func formatOf(f string) string {
	if f == HTMLFormatMJML {
		return HTMLFormatMJML
	}
	return HTMLFormatHTML
}

// compileHTML returns the compiled HTML for an MJML html part ("" for the html format or an empty part).
// The result keeps the Handlebars placeholders: compilation happens once at save, rendering at preview and
// send. A Handlebars source that compilation broke is refused here, never at send time.
func (s *Service) compileHTML(ctx context.Context, format, src string) (string, error) {
	if format != HTMLFormatMJML || src == "" {
		return "", nil
	}
	if strings.TrimSpace(src) == "" {
		return "", fieldProblem("The MJML source is empty.", htmlField, "invalid", []string{"The MJML source is empty."})
	}
	if mjInclude.MatchString(src) {
		msg := "<mj-include> is not supported: paste the included MJML, or use a partial for HTML snippets."
		return "", fieldProblem(msg, htmlField, "invalid", []string{msg})
	}
	start := time.Now()
	out, err := s.mjml().Compile(ctx, src)
	elapsed := time.Since(start)
	if err != nil {
		var ce *mjml.CompileError
		switch {
		case errors.As(err, &ce):
			return "", fieldProblem("Invalid MJML in html: "+ce.Messages[0], htmlField, "invalid", ce.Messages)
		case errors.Is(err, mjml.ErrTimeout):
			s.logger().LogAttrs(ctx, slog.LevelWarn, "mjml compile timed out",
				slog.Int("sourceBytes", len(src)), slog.Duration("elapsed", elapsed))
			msg := "MJML compilation did not finish in time; simplify the template or try again."
			return "", fieldProblem(msg, htmlField, "timeout", []string{msg})
		default:
			return "", errs.Wrap(err, errs.CodeInternalError, "templates.mjml")
		}
	}
	s.logger().LogAttrs(ctx, slog.LevelDebug, "mjml compiled",
		slog.Int("sourceBytes", len(src)), slog.Int("htmlBytes", len(out)), slog.Duration("elapsed", elapsed))
	if len(out) > MaxCompiledHTML {
		msg := "The compiled HTML exceeds 1 MiB (" + strconv.Itoa(len(out)>>10) + " KiB); shorten the template."
		return "", fieldProblem(msg, htmlField, "too_large", []string{msg})
	}
	if _, err := hbs.Parse(out); err != nil {
		msg := "Handlebars in html no longer parses after MJML compilation: " + truncate(err.Error(), 200) +
			" Put block helpers around sections inside <mj-raw>."
		return "", fieldProblem(msg, htmlField, "invalid", []string{msg})
	}
	return out, nil
}

func (s *Service) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// fieldError is a validation_failed error with field-level items: the API answers it as a problem whose
// errors[] lists every item (errs.E has no slot for them).
type fieldError struct {
	e     *errs.E
	items []errs.ProblemItem
}

func (f *fieldError) Error() string { return f.e.Error() }
func (f *fieldError) Unwrap() error { return f.e }

func fieldProblem(detail, path, code string, messages []string) error {
	items := make([]errs.ProblemItem, 0, len(messages))
	for _, m := range messages {
		items = append(items, errs.ProblemItem{Path: path, Code: code, Message: m})
	}
	return &fieldError{e: errs.New(errs.CodeValidationFailed, detail).WithSafe("field", path), items: items}
}

// problemOf renders a fieldError as an RFC 9457 problem with its errors[]; other errors pass through to
// the default mapping.
func problemOf(ctx context.Context, err error) error {
	var fe *fieldError
	if !errors.As(err, &fe) {
		return err
	}
	ids := logging.IDsFrom(ctx)
	p := errs.ProblemFrom(fe.e, ids.RequestID, ids.TraceID)
	p.Errors = fe.items
	return p
}
