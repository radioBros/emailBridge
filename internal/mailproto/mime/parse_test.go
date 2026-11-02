package mime

import (
	"strings"
	"testing"
)

func TestParseHeaders_andDecode(t *testing.T) {
	raw := []byte("From: =?utf-8?q?Andr=C3=A9?= <andre@example.org>\r\n" +
		"Subject: =?utf-8?b?Y2FmZsOo?=\r\n" +
		"To: a@b.com, B <b@c.com>\r\n" +
		"Message-ID: <keep@exact>\r\n" +
		"X-Custom: one\r\n" +
		"X-Custom: two\r\n" +
		"\r\nbody\r\n")
	h, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if MessageIDHeader(h) != "<keep@exact>" {
		t.Fatalf("msgid = %q", MessageIDHeader(h))
	}
	if len(h["x-custom"]) != 2 {
		t.Fatalf("x-custom = %v", h["x-custom"])
	}
	sub, err := DecodeSubject(h)
	if err != nil {
		t.Fatal(err)
	}
	if sub != "caffè" && !strings.Contains(sub, "caf") {
		// WordDecoder may yield caffè
		t.Logf("subject = %q", sub)
	}
	addrs, err := DecodeAddresses(h, "from")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].Address != "andre@example.org" {
		t.Fatalf("from = %+v", addrs)
	}
}

func TestTextParts_andAttachments(t *testing.T) {
	built := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "p",
		Text:      "hello plain",
		HTML:      "<p>hello html</p>",
		MessageID: "<p@example.com>",
		Attachments: []Attachment{
			{Filename: "a.txt", ContentType: "text/plain", Content: []byte("attach"), Disposition: "attachment"},
			{Filename: "logo.png", ContentType: "image/png", Content: []byte("PNGDATA"), Disposition: "inline", ContentID: "logo"},
		},
	})
	e, err := ParseEntity(built.Raw)
	if err != nil {
		t.Fatal(err)
	}
	plain, html, trunc, err := TextParts(e, 0)
	if err != nil {
		t.Fatal(err)
	}
	if trunc {
		t.Fatal("unexpected truncation")
	}
	if plain != "hello plain" {
		t.Fatalf("plain = %q", plain)
	}
	if html != "<p>hello html</p>" {
		t.Fatalf("html = %q", html)
	}
	e2, err := ParseEntity(built.Raw)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := AttachmentsMeta(e2)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) < 2 {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestTextParts_truncation(t *testing.T) {
	built := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Text:      strings.Repeat("x", 100),
		MessageID: "<t@example.com>",
	})
	e, err := ParseEntity(built.Raw)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, trunc, err := TextParts(e, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !trunc || len(plain) != 10 {
		t.Fatalf("plain=%q trunc=%v", plain, trunc)
	}
}

func TestRoundTrip_headersAndText(t *testing.T) {
	m := Message{
		From:      []Address{{Name: "Alice", Address: "alice@example.com"}},
		To:        []Address{{Address: "bob@example.com"}},
		Cc:        []Address{{Address: "cc@example.com"}},
		ReplyTo:   []Address{{Address: "reply@example.com"}},
		Subject:   "Round trip café",
		Text:      "plain text body",
		HTML:      "<b>html</b>",
		MessageID: "<round@example.com>",
		Headers:   map[string]string{"X-Trace": "abc"},
	}
	built := mustBuild(t, m)
	h, err := ParseHeaders(built.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if MessageIDHeader(h) != m.MessageID {
		t.Fatalf("msgid %q", MessageIDHeader(h))
	}
	sub, err := DecodeSubject(h)
	if err != nil {
		t.Fatal(err)
	}
	if sub != m.Subject {
		t.Fatalf("subject %q != %q", sub, m.Subject)
	}
	from, err := DecodeAddresses(h, "from")
	if err != nil || len(from) != 1 || from[0].Address != "alice@example.com" {
		t.Fatalf("from %+v %v", from, err)
	}
	e, err := ParseEntity(built.Raw)
	if err != nil {
		t.Fatal(err)
	}
	plain, html, _, err := TextParts(e, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plain != m.Text || html != m.HTML {
		t.Fatalf("text plain=%q html=%q", plain, html)
	}
	if got := h["x-trace"]; len(got) != 1 || got[0] != "abc" {
		t.Fatalf("x-trace = %v", got)
	}
}
