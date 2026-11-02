package connect

import (
	"strings"
	"testing"
)

func TestSanitizeHeaderHTML_xssCorpus(t *testing.T) {
	payloads := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<div onclick="alert(1)">x</div>`,
		`<p style="background:url(javascript:alert(1))">x</p>`,
		`<iframe src="https://evil.test"></iframe>`,
		`<svg onload=alert(1)>`,
	}
	for _, p := range payloads {
		got := SanitizeHeaderHTML(p)
		low := strings.ToLower(got)
		for _, bad := range []string{"<script", "onerror", "onclick", "javascript:", "<iframe", "onload="} {
			if strings.Contains(low, bad) {
				t.Fatalf("payload %q survived as %q (contains %q)", p, got, bad)
			}
		}
	}
	ok := SanitizeHeaderHTML(`<h2>Welcome</h2><p>Sign in to <strong>Acme</strong></p><a href="https://acme.test">home</a>`)
	if !strings.Contains(ok, "Welcome") || !strings.Contains(ok, "https://acme.test") {
		t.Fatalf("safe html stripped: %q", ok)
	}
	_ = SanitizeHeaderHTML(strings.Repeat("a", maxHeaderHTML+100)) // truncated still ok
}

func TestSanitizeHeadHTML_policy(t *testing.T) {
	got := SanitizeHeadHTML(`
<meta charset="utf-8">
<meta http-equiv="refresh" content="0;url=https://evil.test">
<meta http-equiv="Content-Language" content="en">
<link rel="stylesheet" href="https://cdn.example/a.css">
<link rel="stylesheet" href="http://insecure/a.css">
<link rel="icon" href="https://cdn.example/i.ico">
<script>alert(1)</script>
<style>body{color:red} @import url('http://evil/x.css'); expression(alert(1));</style>
`)
	low := strings.ToLower(got)
	if strings.Contains(low, "<script") || strings.Contains(low, "refresh") || strings.Contains(low, "http://insecure") {
		t.Fatalf("dangerous head survived: %q", got)
	}
	if !strings.Contains(low, `rel="stylesheet"`) || !strings.Contains(got, "https://cdn.example/a.css") {
		t.Fatalf("https stylesheet dropped: %q", got)
	}
	if !strings.Contains(low, "content-language") {
		t.Fatalf("safe meta dropped: %q", got)
	}
	if strings.Contains(low, "rel=\"icon\"") {
		t.Fatalf("non-stylesheet link kept: %q", got)
	}
	prev := PreviewBranding(`<p>Hi</p><script>x</script>`, `<meta name="description" content="d">`)
	if strings.Contains(prev.HeaderHTML, "script") || prev.HeadHTML == "" {
		t.Fatalf("%+v", prev)
	}
	hashes := StyleHashes(`<style>p{color:red}</style>`)
	if len(hashes) != 1 || !strings.HasPrefix(hashes[0], "'sha256-") {
		t.Fatalf("%v", hashes)
	}
}
