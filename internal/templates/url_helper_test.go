package templates

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSafeTemplateURLAllowList(t *testing.T) {
	ok := []string{
		"https://example.com/x",
		"http://example.com",
		"mailto:a@b.c",
		"tel:+15551212",
		"/relative/path",
		"./x",
		"../y",
		"?q=1",
		"#frag",
		"path/to",
		"HTTPS://EXAMPLE.COM",
	}
	for _, u := range ok {
		if !safeTemplateURL(u) {
			t.Fatalf("want allow %q", u)
		}
	}
	bad := []string{
		"javascript:alert(1)",
		"JavaScript:alert(1)",
		" javascript:alert(1)",
		"javascript\t:alert(1)",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"data:text/html,x",
		"//evil.example/x",
		"javascript&#58;alert(1)",
		"javascript%3aalert(1)",
		"java\nscript:alert(1)",
		"ftp://x",
		"about:blank",
	}
	for _, u := range bad {
		if safeTemplateURL(u) {
			t.Fatalf("want refuse %q", u)
		}
	}
}

func TestRenderURLHelperAllowList(t *testing.T) {
	out, err := Render(content{Text: `{{url u}}`}, map[string]any{"u": "https://ok.test/a b"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text == "" {
		t.Fatal("empty")
	}
	for _, u := range []string{"vbscript:x", "file:///x", "//host/x", "javascript&#58;x"} {
		_, err := Render(content{Text: `{{url u}}`}, map[string]any{"u": u})
		if errs.CodeOf(err) != errs.CodeTemplateRenderFailed {
			t.Fatalf("%q: %v", u, err)
		}
	}
}
