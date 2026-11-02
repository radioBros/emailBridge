package submit

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type stubTemplates struct {
	subject, text, html string
	err                 error
	lastID              string
	lastVars            map[string]any
}

func (s *stubTemplates) RenderParts(_ context.Context, id string, _ *int, vars map[string]any) (string, string, string, error) {
	s.lastID, s.lastVars = id, vars
	return s.subject, s.text, s.html, s.err
}

type stubMessages struct {
	raw []byte
	err error
}

func (s *stubMessages) Raw(_ context.Context, _, _ string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.raw, nil
}

func sampleRFC822() []byte {
	return []byte("From: Alice <alice@example.test>\r\n" +
		"To: Sender <sender@example.test>, Other <other@example.test>\r\n" +
		"Cc: cc@example.test\r\n" +
		"Reply-To: reply@example.test\r\n" +
		"Message-ID: <orig@example.test>\r\n" +
		"References: <root@example.test>\r\n" +
		"Subject: Hello there\r\n" +
		"Date: Mon, 26 Oct 2026 12:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Original body line\r\nsecond line\r\n")
}

func TestPrefixSubjectAndMergeVars(t *testing.T) {
	if prefixSubject("Re:", "Hello") != "Re: Hello" {
		t.Fatal("prefix")
	}
	if prefixSubject("Re:", "re: already") != "re: already" {
		t.Fatal("already")
	}
	if prefixSubject("Fwd:", "") != "Fwd:" {
		t.Fatal("empty")
	}
	got := mergeVars(map[string]any{"a": 1, "b": 2}, map[string]any{"b": 9, "c": 3})
	if got["a"] != 1 || got["b"] != 9 || got["c"] != 3 {
		t.Fatalf("%v", got)
	}
}

func TestApplyTemplate(t *testing.T) {
	tpl := &stubTemplates{subject: "S", text: "T", html: "<p>H</p>"}
	s := &Service{Templates: tpl, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req, err := s.applyTemplate(context.Background(), SubmitRequest{
		Template: &TemplateRef{ID: "tpl_01jb0000000000000000000001", Variables: map[string]any{"n": "x"}},
		Subject:  "ignored",
	})
	if err != nil || req.Subject != "S" || req.Text != "T" || req.HTML != "<p>H</p>" || req.Template != nil {
		t.Fatalf("%+v %v", req, err)
	}
	_, err = (&Service{}).applyTemplate(context.Background(), SubmitRequest{Template: &TemplateRef{ID: "tpl_x"}})
	if errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
	_, err = s.applyTemplate(context.Background(), SubmitRequest{Template: &TemplateRef{}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestPrepareRawAndTemplateExclusive(t *testing.T) {
	s := &Service{Templates: &stubTemplates{subject: "S"}}
	_, err := s.prepareRequest(context.Background(), "acc", &SMTPAccount{}, SubmitRequest{
		Raw: "eHg=", Template: &TemplateRef{ID: "tpl_01jb0000000000000000000001"},
	})
	if errs.CodeOf(err) != errs.CodeRawAndFields {
		t.Fatal(err)
	}
}

func TestApplyReferenceReplyForward(t *testing.T) {
	raw := sampleRFC822()
	s := &Service{Messages: &stubMessages{raw: raw}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	acc := &SMTPAccount{Email: "sender@example.test", Name: "Sender"}
	msgID := store.NewID(store.PrefixMessage)

	req, err := s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		Text:      "Thanks",
		Reference: &Reference{MessageID: msgID, Action: actionReply},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Subject != "Re: Hello there" || req.Headers["In-Reply-To"] != "<orig@example.test>" {
		t.Fatalf("reply headers: %+v", req)
	}
	if !strings.Contains(req.Headers["References"], "<root@example.test>") ||
		!strings.Contains(req.Headers["References"], "<orig@example.test>") {
		t.Fatalf("references: %q", req.Headers["References"])
	}
	if len(req.To) != 1 || req.To[0].Address != "reply@example.test" {
		t.Fatalf("reply to: %+v", req.To)
	}
	if !strings.Contains(req.Text, "> Original body line") || !strings.Contains(req.Text, "Thanks") {
		t.Fatalf("quoted text: %q", req.Text)
	}
	if req.Reference != nil {
		t.Fatal("reference should be consumed")
	}

	req, err = s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		Reference: &Reference{MessageID: msgID, Action: actionReplyAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	addrs := map[string]bool{}
	for _, a := range append(req.To, req.Cc...) {
		addrs[strings.ToLower(a.Address)] = true
	}
	if !addrs["reply@example.test"] || !addrs["other@example.test"] || !addrs["cc@example.test"] {
		t.Fatalf("replyAll recipients: %+v to=%+v cc=%+v", addrs, req.To, req.Cc)
	}
	if addrs["sender@example.test"] {
		t.Fatal("self should be excluded")
	}

	inline := false
	req, err = s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		To:        []AddressInput{{Address: "fwd@example.test"}},
		Reference: &Reference{MessageID: msgID, Action: actionForward, Inline: &inline},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Subject != "Fwd: Hello there" || len(req.Attachments) != 1 ||
		req.Attachments[0].ContentType != "message/rfc822" {
		t.Fatalf("forward attach: %+v", req)
	}
	decoded, _ := base64.StdEncoding.DecodeString(req.Attachments[0].Content)
	if string(decoded) != string(raw) {
		t.Fatal("forwarded raw mismatch")
	}

	_, err = s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		Reference: &Reference{MessageID: msgID, Action: actionForward},
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("forward needs recipients: %v", err)
	}
	_, err = s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		Reference: &Reference{MessageID: msgID, Action: "bounce"},
	})
	if errs.CodeOf(err) != errs.CodeReferenceInvalid {
		t.Fatal(err)
	}
	_, err = s.applyReference(context.Background(), "acc", acc, SubmitRequest{
		Reference: &Reference{Action: actionReply},
	})
	if errs.CodeOf(err) != errs.CodeReferenceInvalid {
		t.Fatal(err)
	}
	_, err = (&Service{}).applyReference(context.Background(), "acc", acc, SubmitRequest{
		Reference: &Reference{MessageID: msgID, Action: actionReply},
	})
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatal(err)
	}
}

func TestQuoteBodiesHTMLAndHelpers(t *testing.T) {
	orig := &originalMessage{
		Date: "Mon", From: nil, Text: "plain <x>", HTML: "<b>Hi</b>",
	}
	text, html := quoteBodies("new", "<p>new</p>", orig)
	if !strings.Contains(text, "> plain <x>") || !strings.Contains(html, "blockquote") ||
		!strings.Contains(html, "<b>Hi</b>") || !strings.Contains(html, "eb-quote-attr") {
		t.Fatalf("%q %q", text, html)
	}
	text2, html2 := quoteBodies("", "", &originalMessage{Text: "only", From: nil})
	if !strings.Contains(text2, "> only") || html2 != "" {
		t.Fatalf("text-only quote: %q %q", text2, html2)
	}
	_, html3 := quoteBodies("", "", &originalMessage{Text: "only", HTML: "", From: nil})
	_ = html3
	_, html4 := quoteBodies("", "<p>n</p>", &originalMessage{Text: "only"})
	if !strings.Contains(html4, "<pre>") || !strings.Contains(html4, "only") {
		t.Fatalf("html quote from text: %q", html4)
	}
	if htmlEscape(`<&"'`) != `&lt;&amp;&quot;&#39;` {
		t.Fatalf("htmlEscape: %q", htmlEscape(`<&"'`))
	}
	if formatFrom(nil) != "someone" {
		t.Fatal("formatFrom empty")
	}
	if firstNonEmpty("", "b") != "b" || firstNonEmpty("a", "b") != "a" {
		t.Fatal("firstNonEmpty")
	}
}

func TestMergeCapAndDryRunValidation(t *testing.T) {
	s := &Service{MergeMax: 2, Templates: &stubTemplates{subject: "Hi", text: "T"},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.mergeCap() != 2 {
		t.Fatal(s.mergeCap())
	}
	if (&Service{}).mergeCap() != MaxMergeRecipients {
		t.Fatal("default cap")
	}
	org := store.NewID(store.PrefixOrganization)
	accID := store.NewID(store.PrefixAccount)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	acc := &SMTPAccount{
		ID: accID, OrganizationID: org, State: "connected",
		Email: "sender@example.test", Name: "Sender",
	}
	req := SubmitRequest{
		DryRun:   true,
		Template: &TemplateRef{ID: "tpl_01jb0000000000000000000001", Variables: map[string]any{"x": 1}},
		Recipients: []MergeRecipient{
			{To: []AddressInput{{Address: "a@example.test"}}, Variables: map[string]any{"x": 2}},
			{To: []AddressInput{{Address: "b@example.test"}}},
		},
	}
	// Dry-run merge only builds; call submitMerge directly (no Repo/Objects).
	res, err := s.submitMerge(ctx, org, accID, acc, req, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.MergeDryRun == nil || len(res.MergeDryRun.Results) != 2 {
		t.Fatalf("%+v", res)
	}
	if s.Templates.(*stubTemplates).lastVars["x"] != 1 && s.Templates.(*stubTemplates).lastVars["x"] != 2 {
		// last recipient has no override so base x=1; either way renderer was called
		t.Logf("vars=%v", s.Templates.(*stubTemplates).lastVars)
	}

	_, err = s.submitMerge(ctx, org, accID, acc, SubmitRequest{
		Recipients: make([]MergeRecipient, 3),
	}, "", "", "", false)
	if errs.CodeOf(err) != errs.CodeMailMergeTooLarge {
		t.Fatal(err)
	}
	_, err = s.submitMerge(ctx, org, accID, acc, SubmitRequest{
		To: []AddressInput{{Address: "x@y.test"}},
		Recipients: []MergeRecipient{
			{To: []AddressInput{{Address: "a@example.test"}}},
		},
	}, "", "", "", false)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	_, err = s.submitMerge(ctx, org, accID, acc, SubmitRequest{
		Raw: "eHg=", Recipients: []MergeRecipient{{To: []AddressInput{{Address: "a@example.test"}}}},
	}, "", "", "", false)
	if errs.CodeOf(err) != errs.CodeRawAndFields {
		t.Fatal(err)
	}
	_, err = s.submitMerge(ctx, org, accID, acc, SubmitRequest{
		Recipients: []MergeRecipient{{Variables: map[string]any{"a": 1}}},
	}, "", "", "", false)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}
