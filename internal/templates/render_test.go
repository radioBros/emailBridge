package templates

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestRenderHTMLEscapesTextDoesNot(t *testing.T) {
	c := content{
		Subject: "Hi {{name}}",
		Text:    "Hi {{name}} <tag>",
		HTML:    "<p>Hi {{name}}</p>",
	}
	out, err := Render(c, map[string]any{"name": `<Ada & "Bob">`})
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != `Hi <Ada & "Bob">` {
		t.Fatalf("subject (no escape): %q", out.Subject)
	}
	if out.Text != `Hi <Ada & "Bob"> <tag>` {
		t.Fatalf("text (no escape): %q", out.Text)
	}
	if !strings.Contains(out.HTML, "&lt;Ada &amp; &quot;Bob&quot;&gt;") &&
		!strings.Contains(out.HTML, "&lt;Ada &amp; &apos;") {
		// raymond uses &apos; for ' and &quot; for "
		if !strings.Contains(out.HTML, "Ada") || !strings.Contains(out.HTML, "&lt;") {
			t.Fatalf("html should escape: %q", out.HTML)
		}
	}
}

func TestRenderStrictMissingVariable(t *testing.T) {
	_, err := Render(content{Text: "Hello {{name}}", Strict: true}, map[string]any{})
	if errs.CodeOf(err) != errs.CodeTemplateVariableMissing {
		t.Fatalf("code=%s err=%v", errs.CodeOf(err), err)
	}
	out, err := Render(content{Text: "Hello {{name}}", Strict: false}, map[string]any{})
	if err != nil || out.Text != "Hello " {
		t.Fatalf("non-strict: %q %v", out.Text, err)
	}
}

func TestRenderPartialsAndHelpers(t *testing.T) {
	c := content{
		Text:     `{{upper name}} {{lower name}} {{default empty "x"}} {{> sig}} {{date when format="2006-01-02" tz="UTC"}}`,
		Partials: map[string]string{"sig": "-- {{name}}"},
		Timezone: "UTC",
		Locale:   "en",
	}
	when := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	out, err := Render(c, map[string]any{"name": "Ada", "empty": "", "when": when})
	if err != nil {
		t.Fatal(err)
	}
	want := "ADA ada x -- Ada 2026-10-26"
	if out.Text != want {
		t.Fatalf("got %q want %q", out.Text, want)
	}
}

func TestRenderURLHelper(t *testing.T) {
	out, err := Render(content{Text: `{{url u}}`}, map[string]any{"u": "a b"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "a+b" && out.Text != "a%20b" {
		t.Fatalf("url escape: %q", out.Text)
	}
	_, err = Render(content{Text: `{{url u}}`}, map[string]any{"u": "javascript:alert(1)"})
	if errs.CodeOf(err) != errs.CodeTemplateRenderFailed {
		t.Fatalf("unsafe url: %s %v", errs.CodeOf(err), err)
	}
}

func TestValidateSources(t *testing.T) {
	if err := ValidateSources(content{Subject: "{{ok}}", Text: "hi", HTML: "<b>{{x}}</b>",
		Partials: map[string]string{"p": "{{y}}"}}); err != nil {
		t.Fatal(err)
	}
	err := ValidateSources(content{Text: "{{#if"})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad parse: %v", err)
	}
}

func TestRenderNilVarsAndEmptyParts(t *testing.T) {
	out, err := Render(content{Subject: "static"}, nil)
	if err != nil || out.Subject != "static" || out.Text != "" || out.HTML != "" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestMapRenderErrTruncate(t *testing.T) {
	long := strings.Repeat("x", 300)
	err := mapRenderErr(errs.New(errs.CodeInternalError, long))
	if errs.CodeOf(err) != errs.CodeTemplateRenderFailed {
		t.Fatal(err)
	}
	if truncate("ab", 5) != "ab" || truncate("abcdef", 3) != "abc" {
		t.Fatal("truncate")
	}
	if mapRenderErr(nil) != nil {
		t.Fatal("nil")
	}
}
