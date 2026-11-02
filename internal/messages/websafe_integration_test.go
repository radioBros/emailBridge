//go:build integration

package messages

import (
	"strings"
	"testing"
	"time"
)

// The message view carries webSafeHtml next to html: readers display the sanitised form (P4.2).
func TestIntegrationWebSafeHTMLInView(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := []byte("From: a@b.test\r\nTo: c@d.test\r\nSubject: html\r\nMessage-ID: <xss@x>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n<p onclick=\"steal()\">Hi<script>alert(1)</script></p>\r\n")
	up, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", up.UID, up.UIDValidity, "<xss@x>", nil, int64(len(raw)))
	v, err := h.svc.GetMessage(ctx, h.acc.ID, msgID, GetOptions{Text: "html"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.HTML, "<script>") {
		t.Fatalf("html is the original: %q", v.HTML)
	}
	if v.WebSafeHTML == "" || strings.Contains(v.WebSafeHTML, "<script") || strings.Contains(v.WebSafeHTML, "onclick") || !strings.Contains(v.WebSafeHTML, "Hi") {
		t.Fatalf("webSafeHtml: %q", v.WebSafeHTML)
	}
}
