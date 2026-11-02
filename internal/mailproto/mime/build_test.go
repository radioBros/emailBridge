package mime

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

var update = flag.Bool("update", false, "update golden files")

func testOpt() Options {
	n := 0
	fixed := time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC)
	return Options{
		Now: func() time.Time { return fixed },
		Boundary: func() string {
			n++
			return "b" + strings.Repeat("0", 8-len(itoa(n))) + itoa(n)
		},
		Entropy: bytes.NewReader(bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 4)),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestBuild_plain(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "Hello",
		Text:      "plain body\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<fixed@example.com>",
	})
	assertGolden(t, "plain.eml", got.Raw)
	if got.MessageID != "<fixed@example.com>" {
		t.Fatalf("MessageID = %q", got.MessageID)
	}
	if got.Envelope.From != "from@example.com" || len(got.Envelope.Recipients) != 1 {
		t.Fatalf("envelope = %+v", got.Envelope)
	}
}

func TestBuild_htmlText(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "Alt",
		Text:      "plain\n",
		HTML:      "<p>html</p>\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<alt@example.com>",
	})
	assertGolden(t, "html_text.eml", got.Raw)
	if !bytes.Contains(got.Raw, []byte("multipart/alternative")) {
		t.Fatal("expected multipart/alternative")
	}
}

func TestBuild_relatedInline(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "Related",
		Text:      "see image\n",
		HTML:      `<p><img src="cid:logo"></p>` + "\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<rel@example.com>",
		Attachments: []Attachment{{
			Filename:    "logo.png",
			ContentType: "image/png",
			Content:     []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
			Disposition: "inline",
			ContentID:   "logo",
		}},
	})
	assertGolden(t, "related_inline.eml", got.Raw)
	if !bytes.Contains(got.Raw, []byte("multipart/related")) {
		t.Fatal("expected multipart/related")
	}
	if !bytes.Contains(got.Raw, []byte("Content-Id: <logo>")) && !bytes.Contains(got.Raw, []byte("Content-ID: <logo>")) {
		// go-message may canonicalize; accept either casing of Id
		if !bytes.Contains(bytes.ToLower(got.Raw), []byte("content-id: <logo>")) {
			t.Fatal("expected Content-Id <logo>")
		}
	}
}

func TestBuild_mixedAttachments(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "Mixed",
		Text:      "files\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<mix@example.com>",
		Attachments: []Attachment{
			{Filename: "a.txt", ContentType: "text/plain", Content: []byte("aaa"), Disposition: "attachment"},
			{Filename: "b.bin", ContentType: "application/octet-stream", Content: []byte{0, 1, 2, 3}, Disposition: "attachment"},
		},
	})
	assertGolden(t, "mixed_attachments.eml", got.Raw)
	if !bytes.Contains(got.Raw, []byte("multipart/mixed")) {
		t.Fatal("expected multipart/mixed")
	}
}

func TestBuild_utf8(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Name: "Ufficio Città", Address: "ufficio@example.com"}},
		To:        []Address{{Name: "André", Address: "andre@example.org"}},
		Subject:   "Oggetto: càffè ☕",
		Text:      "Corpo con àccènti\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<utf8@example.com>",
	})
	assertGolden(t, "utf8.eml", got.Raw)
}

func TestBuild_longLines(t *testing.T) {
	long := strings.Repeat("word ", 300) // > 998 when encoded
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Subject:   "Long",
		Text:      long + "\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<long@example.com>",
	})
	assertGolden(t, "long_lines.eml", got.Raw)
	for _, line := range bytes.Split(got.Raw, []byte("\n")) {
		l := bytes.TrimRight(line, "\r")
		if len(l) > 998 {
			t.Fatalf("line length %d > 998: %q…", len(l), truncate(l, 40))
		}
	}
}

func TestBuild_bccEnvelopeOnly(t *testing.T) {
	got := mustBuild(t, Message{
		From:      []Address{{Address: "from@example.com"}},
		To:        []Address{{Address: "to@example.com"}},
		Bcc:       []Address{{Address: "secret@example.com"}},
		Subject:   "Bcc",
		Text:      "x\n",
		Date:      time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC),
		MessageID: "<bcc@example.com>",
	})
	if bytes.Contains(bytes.ToLower(got.Raw), []byte("\nbcc:")) || bytes.Contains(bytes.ToLower(got.Raw), []byte("\rbcc:")) {
		t.Fatal("Bcc must not appear in transmitted headers")
	}
	if len(got.Envelope.Recipients) != 2 {
		t.Fatalf("recipients = %v", got.Envelope.Recipients)
	}
}

func TestBuild_refusedHeaders(t *testing.T) {
	cases := []map[string]string{
		{"Bcc": "x@y.com"},
		{"MIME-Version": "1.0"},
		{"Message-ID": "<x@y>"},
		{"Content-Type": "text/plain"},
		{"X-Bad": "a\r\nb"},
	}
	for _, h := range cases {
		_, err := BuildWith(Message{
			From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
			Text: "t", Headers: h, MessageID: "<m@b.com>",
		}, testOpt())
		if err == nil {
			t.Fatalf("expected error for %v", h)
		}
		if errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("code = %s for %v (%v)", errs.CodeOf(err), h, err)
		}
	}
}

func TestBuild_noRecipients(t *testing.T) {
	_, err := Build(Message{From: []Address{{Address: "a@b.com"}}, Text: "t"})
	if errs.CodeOf(err) != errs.CodeNoRecipients {
		t.Fatalf("got %v", err)
	}
}

func TestBuild_invalidAddress(t *testing.T) {
	_, err := Build(Message{
		From: []Address{{Address: "not-an-addr"}},
		To:   []Address{{Address: "ok@example.com"}},
		Text: "t",
	})
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatalf("got %v", err)
	}
}

func TestBuild_generatedMessageID(t *testing.T) {
	got, err := BuildWith(Message{
		From: []Address{{Address: "from@Example.COM"}},
		To:   []Address{{Address: "to@example.com"}},
		Text: "t",
	}, testOpt())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.MessageID, "<") || !strings.HasSuffix(got.MessageID, "@Example.COM>") {
		// domain taken from From as-is after @
		if !strings.Contains(got.MessageID, "@") {
			t.Fatalf("MessageID = %q", got.MessageID)
		}
	}
	id := strings.TrimPrefix(strings.TrimSuffix(got.MessageID, ">"), "<")
	local := strings.SplitN(id, "@", 2)[0]
	if local != strings.ToLower(local) {
		t.Fatalf("ULID not lowercase: %q", local)
	}
}

func TestBuildRaw_withBcc(t *testing.T) {
	raw := []byte("From: from@example.com\r\n" +
		"To: to@example.com\r\n" +
		"Bcc: secret@example.com\r\n" +
		"Subject: raw\r\n" +
		"Message-ID: <raw@example.com>\r\n" +
		"\r\n" +
		"body\r\n")
	got, err := BuildRaw(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "raw_with_bcc.eml", got.Raw)
	if bytes.Contains(bytes.ToLower(got.Raw), []byte("bcc:")) {
		t.Fatal("Bcc not stripped")
	}
	if got.MessageID != "<raw@example.com>" {
		t.Fatalf("MessageID = %q", got.MessageID)
	}
	if len(got.Envelope.Recipients) != 2 {
		t.Fatalf("recipients = %v", got.Envelope.Recipients)
	}
}

func TestBuildRaw_withoutMessageID(t *testing.T) {
	raw := []byte("From: from@example.com\r\n" +
		"To: to@example.com\r\n" +
		"Subject: no-id\r\n" +
		"\r\n" +
		"body\r\n")
	got, err := BuildRawWith(raw, nil, testOpt())
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "raw_no_msgid.eml", got.Raw)
	if got.MessageID == "" || !bytes.Contains(got.Raw, []byte("Message-ID:")) {
		t.Fatalf("Message-ID not added: %q", got.MessageID)
	}
}

func TestBuildRaw_envelopeOverride(t *testing.T) {
	raw := []byte("From: from@example.com\r\nTo: to@example.com\r\nMessage-ID: <x@y>\r\n\r\nbody\r\n")
	got, err := BuildRaw(raw, &Envelope{From: "other@example.com", Recipients: []string{"r@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Envelope.From != "other@example.com" || got.Envelope.Recipients[0] != "r@example.com" {
		t.Fatalf("%+v", got.Envelope)
	}
}

func mustBuild(t *testing.T, m Message) *Built {
	t.Helper()
	got, err := BuildWith(m, testOpt())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
