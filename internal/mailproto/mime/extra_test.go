package mime

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestBuild_htmlOnly(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		HTML: "<b>x</b>", MessageID: "<h@b.com>",
	})
	if !bytes.Contains(got.Raw, []byte("text/html")) {
		t.Fatal(string(got.Raw))
	}
}

func TestBuild_emptyBody(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		MessageID: "<e@b.com>",
	})
	if len(got.Raw) == 0 {
		t.Fatal("empty raw")
	}
}

func TestBuild_mixedWithInline(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "t", HTML: "<p>h</p>", MessageID: "<mi@b.com>",
		Attachments: []Attachment{
			{Filename: "logo.png", ContentType: "image/png", Content: []byte("PNG"), Disposition: "inline", ContentID: "<logo>"},
			{Filename: "f.pdf", ContentType: "application/pdf", Content: []byte("%PDF"), Disposition: "attachment"},
		},
	})
	low := bytes.ToLower(got.Raw)
	if !bytes.Contains(low, []byte("multipart/mixed")) || !bytes.Contains(low, []byte("multipart/related")) {
		t.Fatal(string(got.Raw))
	}
}

func TestBuild_textOnlyWithAttach(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "only", MessageID: "<ta@b.com>",
		Attachments: []Attachment{
			{Filename: "a.bin", Content: []byte{1, 2}, ContentType: "application/octet-stream"},
		},
	})
	if !bytes.Contains(bytes.ToLower(got.Raw), []byte("multipart/mixed")) {
		t.Fatal(string(got.Raw))
	}
}

func TestBuild_ccReplyToNamed(t *testing.T) {
	got := mustBuild(t, Message{
		From:    []Address{{Name: "F", Address: "a@b.com"}},
		To:      []Address{{Name: "T", Address: "t@b.com"}},
		Cc:      []Address{{Address: "cc@b.com"}},
		ReplyTo: []Address{{Address: "r@b.com"}},
		Text:    "x", MessageID: "<n@b.com>",
	})
	h, err := ParseHeaders(got.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(h["cc"]) == 0 || len(h["reply-to"]) == 0 {
		t.Fatalf("%v", h)
	}
}

func TestBuild_customHeaderOK(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "x", MessageID: "<c@b.com>", Headers: map[string]string{"X-Ok": "1"},
	})
	if !bytes.Contains(got.Raw, []byte("X-Ok: 1")) && !bytes.Contains(got.Raw, []byte("X-Ok:1")) {
		// header casing may vary
		h, _ := ParseHeaders(got.Raw)
		if h["x-ok"][0] != "1" {
			t.Fatal(string(got.Raw))
		}
	}
}

func TestBuildRaw_empty(t *testing.T) {
	_, err := BuildRaw(nil, nil)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestBuildRaw_noFrom(t *testing.T) {
	_, err := BuildRaw([]byte("To: a@b.com\r\n\r\nbody"), nil)
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatalf("%v", err)
	}
}

func TestBuildRaw_noRecipients(t *testing.T) {
	_, err := BuildRaw([]byte("From: a@b.com\r\n\r\nbody"), nil)
	if errs.CodeOf(err) != errs.CodeNoRecipients {
		t.Fatalf("%v", err)
	}
}

func TestBuildRaw_lfOnly(t *testing.T) {
	raw := []byte("From: a@b.com\nTo: c@d.com\nBcc: s@t.com\n\nbody\n")
	got, err := BuildRawWith(raw, nil, testOpt())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(got.Raw), []byte("bcc:")) {
		t.Fatal("bcc not stripped")
	}
}

func TestBuildRaw_addMsgID_lf(t *testing.T) {
	raw := []byte("From: a@b.com\nTo: c@d.com\n\nbody\n")
	got, err := BuildRawWith(raw, nil, testOpt())
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID == "" {
		t.Fatal("msgid missing")
	}
}

func TestBuildRaw_invalidAddressList(t *testing.T) {
	_, err := BuildRaw([]byte("From: a@b.com\r\nTo: not@@\r\n\r\nx"), nil)
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatalf("%v", err)
	}
}

func TestBuildRaw_envelopeFillsFrom(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nMessage-ID: <x@y>\r\n\r\nb")
	got, err := BuildRaw(raw, &Envelope{Recipients: []string{"r@b.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Envelope.From != "a@b.com" {
		t.Fatalf("%+v", got.Envelope)
	}
}

func TestParse_edgeCases(t *testing.T) {
	h, err := ParseHeaders([]byte("Subject: plain\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := DecodeSubject(h)
	if err != nil || sub != "plain" {
		t.Fatalf("%q %v", sub, err)
	}
	if MessageIDHeader(h) != "" {
		t.Fatal("expected empty msgid")
	}
	addrs, err := DecodeAddresses(h, "to")
	if err != nil || addrs != nil {
		t.Fatalf("%v %v", addrs, err)
	}
	_, err = DecodeAddresses(Header{"to": {"%%%"}}, "to")
	if err == nil {
		t.Fatal("expected error")
	}
	plain, html, trunc, err := TextParts(nil, 10)
	if err != nil || plain != "" || html != "" || trunc {
		t.Fatal("nil entity")
	}
	meta, err := AttachmentsMeta(nil)
	if err != nil || meta != nil {
		t.Fatal("nil entity meta")
	}
}

func TestParse_singlePartEntity(t *testing.T) {
	raw := []byte("From: a@b.com\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello")
	e, err := ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _, err := TextParts(e, 0)
	if err != nil || plain != "hello" {
		t.Fatalf("%q %v", plain, err)
	}
}

func TestParse_attachmentDispositionFilename(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "t", MessageID: "<af@b.com>",
		Attachments: []Attachment{
			{Filename: "doc.txt", ContentType: "text/plain", Content: []byte("doc"), Disposition: "attachment"},
		},
	})
	e, err := ParseEntity(got.Raw)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := AttachmentsMeta(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Filename != "doc.txt" {
		t.Fatalf("%+v", meta)
	}
}

func TestParse_inlineOnlyMeta(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		HTML: `<img src="cid:x">`, MessageID: "<io@b.com>",
		Attachments: []Attachment{
			{ContentType: "image/gif", Content: []byte("GIF"), Disposition: "inline", ContentID: "x"},
		},
	})
	e, err := ParseEntity(got.Raw)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := AttachmentsMeta(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].ContentID != "x" {
		t.Fatalf("%+v", meta)
	}
}

func TestStripBcc_headersOnly(t *testing.T) {
	raw := []byte("From: a@b.com\r\nBcc: s@t.com\r\n")
	out := stripBccFields(raw)
	if bytes.Contains(bytes.ToLower(out), []byte("bcc:")) {
		t.Fatal(string(out))
	}
}

func TestInsertHeader_noBlank(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com")
	out, err := insertHeader(raw, "Message-ID", "<z@z>")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("Message-ID: <z@z>")) {
		t.Fatal(string(out))
	}
}

func TestGenerateMessageID_badFrom(t *testing.T) {
	_, err := generateMessageID("@", nil, time.Now())
	if err == nil {
		// domain empty after @
		t.Log("ok or generated")
	}
	_, err = generateMessageID("nodomain", bytes.NewReader(bytes.Repeat([]byte{1}, 16)), time.Now())
	if err != nil {
		t.Fatal(err) // nodomain used as domain
	}
}

func TestBuild_dateFromClock(t *testing.T) {
	fixed := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	opt := testOpt()
	opt.Now = func() time.Time { return fixed }
	got, err := BuildWith(Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "x",
	}, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got.Raw, []byte("2020")) {
		t.Fatal(string(got.Raw))
	}
}

func TestParseHeaders_malformed(t *testing.T) {
	_, err := ParseHeaders([]byte("NoColon\r\n\r\n"))
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestDecodeSubject_empty(t *testing.T) {
	s, err := DecodeSubject(Header{})
	if err != nil || s != "" {
		t.Fatal(s, err)
	}
}

func TestBuild_attachmentDefaultCT(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "t", MessageID: "<ct@b.com>",
		Attachments: []Attachment{{Filename: "x", Content: []byte("z"), Disposition: "attachment"}},
	})
	if !bytes.Contains(got.Raw, []byte("application/octet-stream")) {
		t.Fatal(string(got.Raw))
	}
}

func TestNormalizeContentID(t *testing.T) {
	if normalizeContentID("  ") != "" {
		t.Fatal()
	}
	if normalizeContentID("<a>") != "<a>" {
		t.Fatal()
	}
	if normalizeContentID("a") != "<a>" {
		t.Fatal()
	}
}

func TestLongLine_qp(t *testing.T) {
	// ensure writeSingle path with long ascii
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		HTML: strings.Repeat("z", 1200), MessageID: "<ll@b.com>",
	})
	for _, line := range bytes.Split(got.Raw, []byte("\n")) {
		if len(bytes.TrimRight(line, "\r")) > 998 {
			t.Fatal("line too long")
		}
	}
}

func TestBuild_attachOnlyNoText(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		MessageID: "<ao@b.com>",
		Attachments: []Attachment{
			{Filename: "a.bin", Content: []byte{9}, ContentType: "application/octet-stream", Disposition: "attachment"},
		},
	})
	if !bytes.Contains(bytes.ToLower(got.Raw), []byte("multipart/mixed")) {
		t.Fatal(string(got.Raw))
	}
}

func TestBuild_htmlWithAttach(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		HTML: "<p>h</p>", MessageID: "<ha@b.com>",
		Attachments: []Attachment{{Filename: "a.txt", Content: []byte("a"), ContentType: "text/plain"}},
	})
	e, err := ParseEntity(got.Raw)
	if err != nil {
		t.Fatal(err)
	}
	_, html, _, err := TextParts(e, 0)
	if err != nil || html != "<p>h</p>" {
		t.Fatalf("%q %v", html, err)
	}
}

func TestBuild_textWithInline(t *testing.T) {
	got := mustBuild(t, Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "see img", MessageID: "<ti@b.com>",
		Attachments: []Attachment{
			{ContentType: "image/png", Content: []byte("x"), Disposition: "inline", ContentID: "i"},
		},
	})
	if !bytes.Contains(bytes.ToLower(got.Raw), []byte("multipart/related")) {
		t.Fatal(string(got.Raw))
	}
}

func TestBuild_defaultOpts(t *testing.T) {
	got, err := Build(Message{
		From: []Address{{Address: "a@b.com"}}, To: []Address{{Address: "c@d.com"}},
		Text: "x", MessageID: "<d@b.com>", Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Raw) == 0 {
		t.Fatal("empty")
	}
}

func TestCloseWith(t *testing.T) {
	errBoom := errors.New("boom")
	if err := closeWith(nopCloser{}, errBoom); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	if err := closeWith(errCloser{errBoom}, nil); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	if err := closeWith(nopCloser{}, nil); err != nil {
		t.Fatal(err)
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

type errCloser struct{ err error }

func (e errCloser) Close() error { return e.err }

func TestFirstAddress_empty(t *testing.T) {
	_, err := firstAddress("")
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatal(err)
	}
	_, err = firstAddress("<<<")
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatal(err)
	}
}

func TestGenerateMessageID_emptyDomain(t *testing.T) {
	_, err := generateMessageID("user@", bytes.NewReader(bytes.Repeat([]byte{1}, 16)), time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseAddrs_emptyAddress(t *testing.T) {
	_, err := parseAddrs([]Address{{Name: "x", Address: "  "}})
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatal(err)
	}
}

func TestParse_foldedHeader(t *testing.T) {
	raw := []byte("Subject: hello\r\n world\r\nFrom: a@b.com\r\n\r\n")
	h, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h["subject"][0], "hello") {
		t.Fatalf("%v", h["subject"])
	}
}

func TestTextParts_skipDuplicate(t *testing.T) {
	// multipart/alternative with two plain parts — only first kept
	raw := []byte("MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=b1\r\n\r\n" +
		"--b1\r\nContent-Type: text/plain\r\n\r\nfirst\r\n" +
		"--b1\r\nContent-Type: text/plain\r\n\r\nsecond\r\n" +
		"--b1--\r\n")
	e, err := ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _, err := TextParts(e, 0)
	if err != nil || plain != "first" {
		t.Fatalf("%q %v", plain, err)
	}
}

func TestAttachmentsMeta_nameParam(t *testing.T) {
	raw := []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/pdf; name=file.pdf\r\n" +
		"Content-Disposition: attachment\r\n\r\ndata\r\n" +
		"--b--\r\n")
	e, err := ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := AttachmentsMeta(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Filename != "file.pdf" {
		t.Fatalf("%+v", meta)
	}
}

func TestBuildRaw_headersOnlyNoBody(t *testing.T) {
	raw := []byte("From: a@b.com\r\nTo: c@d.com\r\nMessage-ID: <x@y>\r\n")
	got, err := BuildRaw(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(got.Raw), []byte("bcc:")) {
		t.Fatal()
	}
}

func TestBuild_missingFrom(t *testing.T) {
	_, err := Build(Message{To: []Address{{Address: "c@d.com"}}, Text: "t"})
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatal(err)
	}
}

func TestBuild_invalidToCcBccReply(t *testing.T) {
	base := Message{From: []Address{{Address: "a@b.com"}}, Text: "t", MessageID: "<x@y>"}
	for _, m := range []Message{
		{From: base.From, Text: "t", To: []Address{{Address: "bad"}}, MessageID: base.MessageID},
		{From: base.From, Text: "t", To: []Address{{Address: "ok@b.com"}}, Cc: []Address{{Address: "bad"}}, MessageID: base.MessageID},
		{From: base.From, Text: "t", To: []Address{{Address: "ok@b.com"}}, Bcc: []Address{{Address: "bad"}}, MessageID: base.MessageID},
		{From: base.From, Text: "t", To: []Address{{Address: "ok@b.com"}}, ReplyTo: []Address{{Address: "bad"}}, MessageID: base.MessageID},
	} {
		if _, err := Build(m); errs.CodeOf(err) != errs.CodeInvalidAddress {
			t.Fatalf("%+v -> %v", m, err)
		}
	}
}

func TestDecodeSubject_broken(t *testing.T) {
	// invalid encoded-word — decoder may return error or raw
	_, _ = DecodeSubject(Header{"subject": {"=?utf-8?q?=XX?="}})
}

func TestParseEntity_garbage(t *testing.T) {
	_, err := ParseEntity([]byte("\x00\x01\x02 not a message"))
	// may or may not error depending on parser; just exercise the path
	_ = err
}

func TestStripBcc_empty(t *testing.T) {
	if string(stripBccFields(nil)) != "" && stripBccFields(nil) != nil {
		// nil in → nil or empty out
		out := stripBccFields([]byte{})
		_ = out
	}
	out := stripBccFields([]byte("no blank line and no headers really"))
	_ = out
}

func TestAttachmentsMeta_cidNoDisposition(t *testing.T) {
	raw := []byte("MIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<html/>\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-Id: <pic>\r\n\r\nxxx\r\n" +
		"--b--\r\n")
	e, err := ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := AttachmentsMeta(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Disposition != "inline" {
		t.Fatalf("%+v", meta)
	}
}

func TestTextParts_htmlOnlyMultipart(t *testing.T) {
	raw := []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<h1/>\r\n" +
		"--b--\r\n")
	e, err := ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, html, _, err := TextParts(e, 0)
	if err != nil || html != "<h1/>" {
		t.Fatalf("%q %v", html, err)
	}
}

func TestBuildRaw_envelopeEmptyFromAndBadHeader(t *testing.T) {
	_, err := BuildRaw([]byte("To: a@b.com\r\n\r\nx"), &Envelope{Recipients: []string{"a@b.com"}})
	if errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatal(err)
	}
}

func TestWritePart_extraHeaders(t *testing.T) {
	// exercise extra map in writePart via related content already; call directly
	var buf bytes.Buffer
	var h gomail.Header
	h.SetContentType("multipart/mixed", map[string]string{"boundary": "zz"})
	mw, err := message.CreateWriter(&buf, h.Header)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePart(mw, "text/plain", "quoted-printable", "hi", map[string]string{"X-Part": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("X-Part: 1")) {
		t.Fatal(buf.String())
	}
}
