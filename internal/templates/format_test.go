package templates

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/templates/mjml"
)

type fakeMJML struct {
	out   string
	err   error
	calls int
}

func (f *fakeMJML) Compile(context.Context, string) (string, error) {
	f.calls++
	return f.out, f.err
}

func quietService(c MJMLCompiler) *Service {
	return &Service{MJML: c, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

const invoiceMJML = `<mjml>
  <mj-body>
    <mj-raw>{{#if vip}}</mj-raw>
    <mj-section>
      <mj-column>
        <mj-text>VIP {{customer.firstName}}</mj-text>
      </mj-column>
    </mj-section>
    <mj-raw>{{/if}}</mj-raw>
    <mj-section>
      <mj-column>
        <mj-text>Hello {{default customer.firstName "there"}}, invoice {{invoice.number}} of {{date invoice.issued format="January 2006"}}.
          {{#each items}}<b>{{this}}</b>{{/each}} {{{trusted}}} {{> signature}}</mj-text>
        <mj-button href="https://billing.example.com/i/{{url invoice.number}}?ref={{ref}}&amp;x=1">Open</mj-button>
        <mj-image src="https://cdn.example.com/{{logo}}.png" alt="{{company}}" />
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>`

// MJML first, Handlebars second: every placeholder form survives compilation, also inside attributes.
func TestMJMLHandlebarsSurviveCompilation(t *testing.T) {
	s := quietService(mjml.Default())
	compiled, err := s.compileHTML(context.Background(), HTMLFormatMJML, invoiceMJML)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"{{#if vip}}", "{{/if}}", "{{default customer.firstName \"there\"}}", "{{> signature}}", "{{{trusted}}}",
		"{{#each items}}", `href="https://billing.example.com/i/{{url invoice.number}}?ref={{ref}}`,
		`src="https://cdn.example.com/{{logo}}.png"`, `alt="{{company}}"`,
	} {
		if !strings.Contains(compiled, want) {
			t.Fatalf("compiled HTML lost %q", want)
		}
	}
	out, err := Render(content{
		HTML: compiled, Partials: map[string]string{"signature": "<i>Billing team</i>"}, Locale: "en", Timezone: "UTC",
	}, map[string]any{
		"vip": true, "customer": map[string]any{"firstName": "Anna & Co"},
		"invoice": map[string]any{"number": "2026/1043", "issued": "2026-10-27T09:00:00Z"},
		"items":   []any{"a", "b"}, "trusted": "<u>ok</u>", "ref": "x y", "logo": "brand", "company": "Example \"Ltd\"",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"VIP Anna &amp; Co", "Hello Anna &amp; Co", "invoice 2026/1043 of October 2026", "<b>a</b><b>b</b>",
		"<u>ok</u>", "<i>Billing team</i>", `href="https://billing.example.com/i/2026%2F1043?ref=x y&amp;x=1"`,
		`src="https://cdn.example.com/brand.png"`, `alt="Example &quot;Ltd&quot;"`,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Fatalf("rendered HTML lacks %q:\n%s", want, out.HTML)
		}
	}
	if strings.Contains(out.HTML, "{{") {
		t.Fatal("unrendered placeholder left")
	}
	out, err = Render(content{HTML: compiled, Partials: map[string]string{"signature": ""}}, map[string]any{"vip": false})
	if err != nil || strings.Contains(out.HTML, "VIP") {
		t.Fatalf("block helper around a section: %v", err)
	}
}

func TestCompileHTMLInvalidMJMLFieldErrors(t *testing.T) {
	s := quietService(mjml.Default())
	src := "<mjml>\n<mj-body>\n<mj-section>\n<mj-column>\n<mj-bogus></mj-bogus>\n<mj-image width=\"abc\" src=\"x.png\"/>\n" +
		"</mj-column>\n</mj-section>\n</mj-body>\n</mjml>"
	_, err := s.compileHTML(context.Background(), HTMLFormatMJML, src)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("want validation_failed, got %v", err)
	}
	var fe *fieldError
	if !errors.As(err, &fe) || len(fe.items) != 2 {
		t.Fatalf("want 2 field items, got %v", err)
	}
	for _, it := range fe.items {
		if it.Path != "body.html" || it.Code != "invalid" || !strings.HasPrefix(it.Message, "Line ") {
			t.Fatalf("item: %+v", it)
		}
	}
	if !strings.Contains(fe.e.Msg, "Invalid MJML in html: Line 5, mj-bogus") {
		t.Fatalf("detail: %q", fe.e.Msg)
	}
	var p *errs.Problem
	if !errors.As(problemOf(context.Background(), err), &p) || p.Status != 422 || p.Code != errs.CodeValidationFailed || len(p.Errors) != 2 {
		t.Fatalf("problem: %+v", p)
	}
	plain := errs.New(errs.CodeNotFound, "x")
	if pass := problemOf(context.Background(), plain); !errors.Is(pass, plain) || errors.As(pass, &p) {
		t.Fatal("non-field errors must pass through")
	}
}

func TestCompileHTMLRefusals(t *testing.T) {
	ctx := context.Background()
	fake := &fakeMJML{out: "<html>{{name}}</html>"}
	s := quietService(fake)
	if out, err := s.compileHTML(ctx, HTMLFormatHTML, "<p>{{x}}</p>"); out != "" || err != nil || fake.calls != 0 {
		t.Fatal("html format must not compile")
	}
	if out, err := s.compileHTML(ctx, HTMLFormatMJML, ""); out != "" || err != nil || fake.calls != 0 {
		t.Fatal("empty part must not compile")
	}
	cases := map[string]string{
		"blank":   "  \n ",
		"include": `<mjml><mj-body><mj-include path="/etc/passwd" /></mj-body></mjml>`,
		"INCLUDE": `<mjml><mj-head><MJ-INCLUDE path="./a.css" type="css"/></mj-head></mjml>`,
	}
	for name, src := range cases {
		if _, err := s.compileHTML(ctx, HTMLFormatMJML, src); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if fake.calls != 0 {
		t.Fatal("refused sources must not reach the compiler")
	}
	if out, err := s.compileHTML(ctx, HTMLFormatMJML, "<mjml/>"); err != nil || out != fake.out {
		t.Fatalf("ok: %q %v", out, err)
	}

	fake.err = mjml.ErrTimeout
	_, err := s.compileHTML(ctx, HTMLFormatMJML, "<mjml/>")
	var fe *fieldError
	if !errors.As(err, &fe) || fe.items[0].Code != "timeout" {
		t.Fatalf("timeout: %v", err)
	}
	fake.err = errors.New("wasm trap")
	if _, err := s.compileHTML(ctx, HTMLFormatMJML, "<mjml/>"); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("internal: %v", err)
	}
	fake.err = nil
	fake.out = strings.Repeat("x", MaxCompiledHTML+1)
	if _, err := s.compileHTML(ctx, HTMLFormatMJML, "<mjml/>"); !errors.As(err, &fe) || fe.items[0].Code != "too_large" {
		t.Fatalf("too large: %v", err)
	}
	fake.out = "<html>{{#if x}}</html>"
	if _, err := s.compileHTML(ctx, HTMLFormatMJML, "<mjml/>"); !errors.As(err, &fe) ||
		!strings.Contains(fe.items[0].Message, "after MJML compilation") {
		t.Fatalf("broken handlebars: %v", err)
	}
}

func TestCompileHTMLTimeoutBounded(t *testing.T) {
	s := quietService(mjml.New(1, 5*time.Millisecond))
	big := "<mjml><mj-body>" + strings.Repeat(`<mj-section><mj-column><mj-text>{{name}}</mj-text></mj-column></mj-section>`, 200) +
		"</mj-body></mjml>"
	start := time.Now()
	_, err := s.compileHTML(context.Background(), HTMLFormatMJML, big)
	var fe *fieldError
	if !errors.As(err, &fe) || fe.items[0].Code != "timeout" {
		t.Fatalf("want timeout field error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not bounded")
	}
}

func TestFormatHelpers(t *testing.T) {
	for in, want := range map[string]string{"": "html", "html": "html", " MJML ": "mjml", "mjml": "mjml"} {
		if got, err := normalizeFormat(in); err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	if _, err := normalizeFormat("markdown"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if formatOf("") != HTMLFormatHTML || formatOf("mjml") != HTMLFormatMJML || formatOf("x") != HTMLFormatHTML {
		t.Fatal("formatOf")
	}
	tpl := &Template{ID: "tpl_1", HTML: "<mjml/>", HTMLFormat: HTMLFormatMJML, HTMLCompiled: "<html/>", CurrentVersion: 3}
	if contentOf(tpl).HTML != "<html/>" || sourcesOf(tpl).HTML != "<mjml/>" {
		t.Fatal("contentOf / sourcesOf")
	}
	v := snapshot(tpl, "usr", time.Unix(1, 0).UTC())
	if v.HTMLFormat != HTMLFormatMJML || v.HTMLCompiled != "<html/>" || contentOfVersion(v).HTML != "<html/>" {
		t.Fatalf("version keeps the format: %+v", v)
	}
	legacy := &Version{HTML: "<p>x</p>"}
	if contentOfVersion(legacy).HTML != "<p>x</p>" {
		t.Fatal("pre-G2 versions are html")
	}
	if (&Service{}).mjml() != mjml.Default() || (&Service{}).logger() == nil {
		t.Fatal("defaults")
	}
}
