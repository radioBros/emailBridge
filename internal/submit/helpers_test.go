package submit

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/pecreceipts"
	"github.com/radiobros/emailbridge/internal/submit/policy"
)

// TestServiceDefaults checks the fallbacks of a service configured with nothing (S9 defaults).
func TestServiceDefaults(t *testing.T) {
	s := &Service{}
	if s.now().IsZero() || s.log() == nil {
		t.Fatal("now / log")
	}
	if v := s.rand()(); v < 0 || v >= 1 {
		t.Fatalf("rand = %v", v)
	}
	if s.copyDelay() != 2*time.Second || s.idempotencyTTL() != 24*time.Hour {
		t.Fatalf("copyDelay %s, idempotencyTTL %s", s.copyDelay(), s.idempotencyTTL())
	}
	if got := s.backoffBase(&Message{}, nil); got != 0 {
		t.Fatalf("backoffBase without settings = %s", got)
	}
	if got := s.backoffBase(&Message{}, &SMTPAccount{BackoffBase: time.Minute}); got != time.Minute {
		t.Fatalf("account backoffBase = %s", got)
	}
	if got := s.backoffBase(&Message{BackoffBase: "30s"}, &SMTPAccount{BackoffBase: time.Minute}); got != 30*time.Second {
		t.Fatalf("message backoffBase = %s", got)
	}
	fixed := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	s = &Service{Now: func() time.Time { return fixed }, Rand: func() float64 { return .5 }, CopyRetryDelay: time.Second}
	if !s.now().Equal(fixed) || s.rand()() != .5 || s.copyDelay() != time.Second {
		t.Fatal("injected now / rand / copyDelay")
	}
}

func TestPoolDefaults(t *testing.T) {
	p := &Pool{}
	if p.perAccount() != 2 || p.maxMessages() != 100 || p.idleFor() != 10*time.Minute {
		t.Fatalf("defaults: %d %d %s", p.perAccount(), p.maxMessages(), p.idleFor())
	}
	p = &Pool{PerAccount: 4, MaxMessages: 7, Idle: time.Second}
	if p.perAccount() != 4 || p.maxMessages() != 7 || p.idleFor() != time.Second {
		t.Fatal("configured values")
	}
	p.Close() // no accounts yet
}

func TestWorkerHelpers(t *testing.T) {
	if ehloName("", "set", "later") != "set" || ehloName("", "") != "localhost" {
		t.Fatal("ehloName")
	}
	long := strings.Repeat("x", ResponseMax+64)
	if len(excerpt("  "+long)) != ResponseMax || excerpt(" ok ") != "ok" {
		t.Fatal("excerpt")
	}
	if responseOf(nil) != "" || hostOf(nil) != "" {
		t.Fatal("nil result / account")
	}
	if rejections(nil) != nil || eventRejections(nil) != nil {
		t.Fatal("no rejections")
	}
	if needsUTF8("a@b.test", []string{"c@d.test"}) {
		t.Fatal("ASCII needs no SMTPUTF8")
	}
	if !needsUTF8("a@b.test", []string{"pöstli@d.test"}) {
		t.Fatal("a non-ASCII recipient needs SMTPUTF8")
	}
	stopped := &abort{err: errs.New(errs.CodeInternalError, "gone")}
	if stopped.Error() != "gone" || errs.CodeOf(stopped.Unwrap()) != errs.CodeInternalError {
		t.Fatalf("abort: %v", stopped)
	}
}

func TestResolveAndCopySettings(t *testing.T) {
	if resolvePolicy("", "", "") != policy.Standard {
		t.Fatal("default policy")
	}
	if resolvePolicy("", policy.AtMostOnce, policy.Standard) != policy.AtMostOnce {
		t.Fatal("the account policy wins over the instance default")
	}
	if resolvePolicy("nonsense", "", policy.AtMostOnce) != policy.AtMostOnce {
		t.Fatal("an unknown value falls through")
	}
	if resolveAttempts(nil, 0, 0) != policy.DefaultMaxAttempts || resolveAttempts(nil, 0, 4) != 4 {
		t.Fatal("maxAttempts fallbacks")
	}
	zero := 0
	five := 5
	if resolveAttempts(&zero, 3) != 3 || resolveAttempts(&five, 3) != 5 {
		t.Fatal("per-message maxAttempts")
	}

	gmail := &SMTPAccount{Host: "smtp.gmail.com", CopyToSentPath: "Sent"}
	if got := copySettings(nil, gmail); got.Enabled || got.State != CopySkipped {
		t.Fatalf("auto on a provider that files sent mail: %+v", got)
	}
	own := &SMTPAccount{Host: "mail.example.com"}
	if got := copySettings(nil, own); !got.Enabled || got.State != CopyPending {
		t.Fatalf("auto on a plain server: %+v", got)
	}
	if got := copySettings(nil, &SMTPAccount{CopyToSent: CopyNever}); got.Enabled {
		t.Fatal("never")
	}
	on := true
	if got := copySettings(&CopyToSentInput{Enabled: &on, Path: "INBOX.Sent"}, gmail); !got.Enabled || got.Path != "INBOX.Sent" {
		t.Fatalf("per-message override: %+v", got)
	}
	off := false
	if got := copySettings(&CopyToSentInput{Enabled: &off}, &SMTPAccount{CopyToSent: CopyAlways}); got.Enabled {
		t.Fatal("per-message off")
	}
}

func TestMessageEncodeDecode(t *testing.T) {
	m := &Message{FinalErrorJSON: "{"}
	m.decode()
	if m.FinalError != nil {
		t.Fatal("broken JSON must stay nil")
	}
	m = &Message{FinalError: errs.ProblemFrom(errs.New(errs.CodeSMTPPermanent, "refused"), "", "")}
	if err := m.encode(); err != nil || !strings.Contains(m.FinalErrorJSON, "smtp_permanent") {
		t.Fatalf("encode: %q (%v)", m.FinalErrorJSON, err)
	}
	decoded := &Message{FinalErrorJSON: m.FinalErrorJSON}
	decoded.decode()
	if decoded.FinalError == nil || decoded.FinalError.Code != errs.CodeSMTPPermanent {
		t.Fatalf("decode: %+v", decoded.FinalError)
	}
	m.FinalError = nil
	if err := m.encode(); err != nil || m.FinalErrorJSON != "" {
		t.Fatalf("no final error: %q (%v)", m.FinalErrorJSON, err)
	}
	if problemJSON(errs.New(errs.CodeNotFound, "gone")) == "" {
		t.Fatal("problemJSON")
	}
	if (&Message{}).LastAttempt() != nil {
		t.Fatal("no attempts")
	}
	if (&Message{Attempts: []Attempt{{Stage: "data"}}}).LastAttempt().Stage != "data" {
		t.Fatal("last attempt")
	}
}

func TestEnvelopeAndViewRedaction(t *testing.T) {
	env := Envelope{From: "a@b.test", To: []string{"to@b.test"}, Cc: []string{"cc@b.test"}, BccCount: 2}
	got := env.Recipients([]string{"b1@b.test", "b2@b.test"})
	if len(got) != 4 || got[0] != "to@b.test" || got[3] != "b2@b.test" {
		t.Fatalf("RCPT order: %v", got)
	}
	view := ViewOf(&Message{ID: "out_1", Envelope: env, Bcc: []string{"b1@b.test"}, MessageIDHeader: "<x@y>"})
	if view.MessageID != "<x@y>" || view.Envelope.BccCount != 2 {
		t.Fatalf("view: %+v", view)
	}
	st := &pecreceipts.OutboundPECStatus{State: pecreceipts.StateAccepted}
	view2 := ViewOf(&Message{ID: "out_2", PEC: st})
	if view2.PEC == nil || view2.PEC.State != pecreceipts.StateAccepted {
		t.Fatalf("pec view: %+v", view2.PEC)
	}
	if ViewOf(&Message{ID: "out_3"}).PEC != nil {
		t.Fatal("pec omitempty")
	}
}

func TestListHelpers(t *testing.T) {
	if listLimit(0) != defaultListLimit || listLimit(1000) != maxListLimit || listLimit(7) != 7 {
		t.Fatal("listLimit")
	}
	when := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	m := Message{ID: "out_1", State: StateQueued, CreatedAt: when, SendAt: &when}
	if listSortKey(m, "_id") != "out_1" || listSortKey(m, "state") != StateQueued {
		t.Fatal("listSortKey id / state")
	}
	if listSortKey(m, "createdAt") != when.Format(time.RFC3339Nano) {
		t.Fatal("listSortKey createdAt")
	}
	if listSortKey(m, "sendAt") != when.Format(time.RFC3339Nano) {
		t.Fatal("listSortKey sendAt")
	}
	if listSortKey(Message{}, "sendAt") != nil {
		t.Fatal("listSortKey without sendAt")
	}
	for _, bad := range []string{"", "***", encodeListCursor(listCursor{Field: "state"})} {
		if _, err := decodeListCursor(bad); err == nil {
			t.Fatalf("cursor %q was accepted", bad)
		}
	}
	// A cursor of the wrong field or direction is refused.
	token := encodeListCursor(listCursor{ID: "out_1", Field: "state", Desc: true})
	if _, _, err := listQuery(nil, "_id", -1, 10, token, 0); err == nil {
		t.Fatal("a cursor of another sort field was accepted")
	}
	if _, _, err := listQuery(nil, "createdAt", -1, 10,
		encodeListCursor(listCursor{ID: "out_1", Field: "createdAt", Desc: true, Key: 42}), 0); err == nil {
		t.Fatal("a cursor with a non-time key was accepted")
	}
	if _, _, err := listQuery(nil, "createdAt", -1, 10,
		encodeListCursor(listCursor{ID: "out_1", Field: "createdAt", Desc: true, Key: "yesterday"}), 0); err == nil {
		t.Fatal("a cursor with an unparsable time was accepted")
	}
	if err := checkIfMatch("", 3); err != nil {
		t.Fatal("no If-Match")
	}
	if err := checkIfMatch(formatETag(3), 3); err != nil {
		t.Fatalf("matching If-Match: %v", err)
	}
	for _, bad := range []string{`"v2"`, "nonsense"} {
		if errs.CodeOf(checkIfMatch(bad, 3)) != errs.CodeVersionMismatch {
			t.Fatalf("If-Match %s", bad)
		}
	}
	if !validState(StateOutcomeUnknown) || validState("nope") {
		t.Fatal("validState")
	}
}

func TestBuildHelpers(t *testing.T) {
	if got := attachments([]AttachmentInput{{Filename: "a.txt", Content: "***"}}); got[0].Content != nil {
		t.Fatal("content that is not base64 is dropped")
	}
	if got := attachments([]AttachmentInput{{Filename: "a.txt", Content: "eHg="}}); string(got[0].Content) != "xx" {
		t.Fatal("base64 content")
	}
	if rawSubject([]byte("From: a@b\r\n\r\nbody")) != "" {
		t.Fatal("no subject")
	}
	if rawSubject([]byte("Subject: Hello \r\nFrom: a@b\r\n\r\nbody")) != "Hello" {
		t.Fatal("subject")
	}
	if got := plain([]AddressInput{{Address: "a@b.test"}}); len(got) != 1 || got[0] != "a@b.test" {
		t.Fatal("plain")
	}
}
