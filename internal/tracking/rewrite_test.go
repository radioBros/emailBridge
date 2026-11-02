package tracking

import (
	"strings"
	"testing"
	"time"
)

func TestPrepareRewriteAndPixel(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	html := `<html><body><p>Hi <a href="https://example.test/x">x</a> <a href="mailto:a@b">m</a></p></body></html>`
	res, err := Prepare(key, PrepareInput{
		OrgID: "org_01jbtestorg00000000000000", OutboundID: "out_01jbtestout00000000000000",
		Recipient: "user@example.test", HTML: html, Headers: map[string]string{},
		TrackOpens: true, TrackClicks: true, ListUnsub: true,
		PublicURL: "https://eb.example", ExpireAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Links) != 1 || res.Links[0].URL != "https://example.test/x" {
		t.Fatalf("links: %+v", res.Links)
	}
	if !strings.Contains(res.HTML, "/t/c/") || !strings.Contains(res.HTML, "/t/o/") {
		t.Fatalf("html: %s", res.HTML)
	}
	if !strings.Contains(res.HTML, "mailto:a@b") {
		t.Fatal("mailto rewritten")
	}
	if res.Headers["List-Unsubscribe"] == "" || res.Headers["List-Unsubscribe-Post"] == "" {
		t.Fatalf("headers: %+v", res.Headers)
	}
}

func TestPrepareRequiresPublicURL(t *testing.T) {
	_, err := Prepare([]byte("k"), PrepareInput{TrackOpens: true, HTML: "<p>x</p>"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTrackableURL(t *testing.T) {
	if trackableURL("javascript:alert(1)") || trackableURL("#top") || !trackableURL("https://x.test") {
		t.Fatal("trackable")
	}
}
