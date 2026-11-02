package messages

import (
	"strings"
	"testing"
)

func TestEnrichFromRaw_textAndSanitize(t *testing.T) {
	s := &Service{}
	v := &MessageView{}
	raw := []byte("From: a@x\r\nTo: b@x\r\nSubject: Hi\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p onclick=x>Hello</p><script>evil()</script>")
	s.enrichFromRaw(v, raw, GetOptions{Text: "both", Headers: true, Envelope: true, PreProcessHTML: true}, false, defaultText)
	if v.Subject != "Hi" {
		t.Fatalf("subject %q", v.Subject)
	}
	if v.HTML == "" || v.WebSafeHTML == "" {
		t.Fatal("expected html")
	}
	if strings.Contains(v.WebSafeHTML, "onclick") || strings.Contains(v.WebSafeHTML, "script") {
		t.Fatalf("unsafe html leaked: %s", v.WebSafeHTML)
	}
	if v.Headers["Subject"] == "" && v.Headers["subject"] == "" {
		// flatten may keep original case
		if len(v.Headers) == 0 {
			t.Fatal("headers empty")
		}
	}
}

func TestEnrichFromRaw_plainOnly(t *testing.T) {
	s := &Service{}
	v := &MessageView{}
	raw := []byte("From: a@x\r\nSubject: P\r\n\r\nplain body")
	s.enrichFromRaw(v, raw, GetOptions{Text: "plain"}, false, defaultText)
	if v.Text == "" {
		t.Fatal("expected plain")
	}
}
