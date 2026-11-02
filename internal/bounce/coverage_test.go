package bounce

import (
	"strings"
	"testing"

	"github.com/emersion/go-message"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

func TestBounceTypeFromSubjectBodyEdges(t *testing.T) {
	cases := []struct {
		subj, body string
		want       BounceType
	}{
		{"", "451 try again later", BounceSoft},
		{"", "550 permanent failure", BounceHard},
		{"", "status 4.4.1 host not found", BounceSoft},
		{"", "enhanced 5.1.1", BounceHard},
		{"", "code 421 greylist", BounceSoft},
		{"", "code 553 invalid", BounceHard},
		{"", "nothing useful here", ""},
		{"undeliverable", "mailbox full over quota", BounceSoft},
	}
	for _, c := range cases {
		got := bounceTypeFromSubjectBody(c.subj, c.body)
		if got != c.want {
			t.Fatalf("subj=%q body=%q: got %q want %q", c.subj, c.body, got, c.want)
		}
	}
}

func TestBounceTypeFromStatusEdges(t *testing.T) {
	cases := []struct {
		status, diag, action string
		want                 BounceType
	}{
		{"2.0.0", "", "delivered", ""},
		{"5.1.1 (permanent failure)", "", "failed", BounceHard},
		{"4.2.1 temp", "", "delayed", BounceSoft},
		{"5.7.1", "over quota", "failed", BounceSoft},
		{"", "451 temporary", "failed", BounceSoft},
		{"", "550 does not exist", "failed", BounceHard},
		{"", "mystery", "failed", ""},
		{"x.y.z", "", "failed", ""},
	}
	for _, c := range cases {
		got := bounceTypeFromStatus(c.status, c.diag, c.action)
		if got != c.want {
			t.Fatalf("%q/%q: got %q want %q", c.status, c.diag, got, c.want)
		}
	}
}

func TestHasAutoReplyHeaderVariants(t *testing.T) {
	mk := func(h string) ebmime.Header {
		hdr, err := ebmime.ParseHeaders([]byte(h + "\r\n\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		return hdr
	}
	if !hasAutoReplyHeader(mk("X-Autoreply: yes")) {
		t.Fatal("x-autoreply")
	}
	if hasAutoReplyHeader(mk("X-Autoreply: no")) {
		t.Fatal("x-autoreply no")
	}
	if !hasAutoReplyHeader(mk("X-Autorespond: oof")) {
		t.Fatal("x-autorespond")
	}
	if !hasAutoReplyHeader(mk("Precedence: auto_reply")) {
		t.Fatal("precedence auto_reply")
	}
	if hasAutoReplyHeader(mk("Precedence: junk")) {
		t.Fatal("precedence junk must not count")
	}
	if !hasAutoReplyHeader(mk("Auto-Submitted: auto-replied\r\nX-Auto-Response-Suppress: All")) {
		t.Fatal("suppress+auto")
	}
	if hasAutoReplyHeader(mk("X-Auto-Response-Suppress: All")) {
		t.Fatal("suppress alone should not count")
	}
}

func TestIsListMessage(t *testing.T) {
	mk := func(h string) ebmime.Header {
		hdr, err := ebmime.ParseHeaders([]byte(h + "\r\n\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		return hdr
	}
	if !isListMessage(mk("List-Id: <x.example.com>")) {
		t.Fatal("list-id")
	}
	if !isListMessage(mk("List-Unsubscribe: <mailto:u@example.com>")) {
		t.Fatal("list-unsub")
	}
	if isListMessage(mk("Precedence: bulk")) {
		t.Fatal("bulk alone must not be list")
	}
	if !isListMessage(mk("Precedence: list")) {
		t.Fatal("list")
	}
	if isListMessage(mk("Subject: hi")) {
		t.Fatal("plain")
	}
}

func TestExactMTASenderAndNullRP(t *testing.T) {
	if isDaemonLocal("support-mailer-daemon") {
		t.Fatal("suffix substring must not match")
	}
	if !isDaemonLocal("mailer-daemon") || !isDaemonLocal("MAILER-DAEMON") {
		t.Fatal("exact")
	}
	if !isDaemonLocal("mailer-daemon+tag") || !isDaemonLocal("double-bounce") {
		t.Fatal("sub-address / double-bounce")
	}
	if !isDaemonDisplayName("Mail Delivery System") {
		t.Fatal("display")
	}
	hdr, err := ebmime.ParseHeaders([]byte("Return-Path: <>\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !isNullReturnPath(hdr) {
		t.Fatal("null rp")
	}
	if !hasSenderEvidence(hdr, nil) {
		t.Fatal("null rp is sender evidence")
	}
}

func TestHumanReplyQuoteHelpers(t *testing.T) {
	if !isHumanReplyWithQuote("Re: hello", "> quoted\n") {
		t.Fatal("re quote")
	}
	if isHumanReplyWithQuote("Undelivered", "no quotes here") {
		t.Fatal("not reply")
	}
	if !hasQuotedHeaderBlock("see original:\nFrom: a@example.com\nMessage-ID: <x@example.com>\n\nbody") {
		t.Fatal("header block")
	}
	if !hasQuotedHeaderBlock("--- Below this line is a copy of the message.\nHi") {
		t.Fatal("marker")
	}
	if !hasForwardBodyMarker("Begin forwarded message:\nFrom: x") {
		t.Fatal("fwd marker")
	}
	if !isForwardSubject("fw: undelivered") || !isForwardSubject("wg: test") {
		t.Fatal("fwd subj")
	}
	if !hasMTATemplate("This is the mail system at host mx.example.com.") {
		t.Fatal("template")
	}
	if hasMTATemplate("hello world") {
		t.Fatal("no template")
	}
	if !isDaemonLocal("bounces") || !isDaemonLocal("mailer-daemon-xyz") {
		t.Fatal("daemon variants")
	}
	if trunc(strings.Repeat("x", 600), 512) == "" {
		t.Fatal("trunc")
	}
	if joinFrom([]ebmime.Address{{Name: "A", Address: "a@example.com"}}) == "" {
		t.Fatal("join")
	}
	if !looksLikeBounceSubject("undeliverable: test") {
		t.Fatal("subj")
	}
	if bounceTypeFromSubjectBody("", "451 try again later") != BounceSoft {
		t.Fatal("soft")
	}
	st, dg := extractStatusDiag("host said: 550 5.1.1 User unknown")
	if st == "" && dg == "" {
		t.Fatal("status")
	}
	addrs := extractRecipientHints("<a@example.com>: host mx said: 550 5.1.1\n", nil)
	if len(addrs) == 0 {
		t.Fatal("recipients")
	}
	if guessMTA(nil, "Undelivered Mail Returned to Sender", "postfix") == "" &&
		guessMTA(ebmime.Header{}, "x", "this is the mail system at host") == "" {
		t.Fatal("guess")
	}
	if localPart("bob@example.com") != "bob" {
		t.Fatal("localPart")
	}
	if localPart("<carol@example.com>") != "carol" {
		t.Fatal("angled localPart")
	}
	if localPart("") != "" {
		t.Fatal("empty local")
	}
	if !hasQuotedReplyBody("On Mon, Alice wrote:\n> hi") {
		t.Fatal("on wrote")
	}
	if !bodyLooksAutoReply("Ich bin bis Freitag nicht im Büro.") {
		t.Fatal("ooo body")
	}
	if bodyLooksAutoReply("hello") {
		t.Fatal("no ooo body")
	}
	// Header-block body evidence + daemon sender
	hdr := ebmime.Header{"x-failed-recipients": {"z@example.com"}}
	from := []ebmime.Address{{Name: "Mail Delivery System", Address: "noreply@mx.example.com"}}
	body := "see below\n----- Original Message -----\nFrom: a@example.com\nMessage-ID: <z@example.com>\n\nHi\n"
	if classifyHeuristicBounce(hdr, "notice", from, body) == nil {
		t.Fatal("header block body evidence")
	}
	if bounceTypeFromSubjectBody("", "550 5.1.1 user unknown") != BounceHard {
		t.Fatal("hard bt")
	}
	if bounceTypeFromSubjectBody("", "code 421 greylist") != BounceSoft {
		t.Fatal("soft code")
	}
}

func TestContentTypeFallback(t *testing.T) {
	hdr := ebmime.Header{"content-type": {"text/plain; charset=utf-8"}}
	ct, params := contentType(hdr, nil)
	if ct != "text/plain" || params["charset"] != "utf-8" {
		t.Fatalf("got %q %v", ct, params)
	}
	hdr2 := ebmime.Header{}
	ct, _ = contentType(hdr2, nil)
	if ct != "text/plain" {
		t.Fatalf("default %q", ct)
	}
	hdr3 := ebmime.Header{"content-type": {"%%%bad%%%"}}
	ct, _ = contentType(hdr3, nil)
	if !strings.Contains(ct, "bad") {
		t.Fatalf("bad ct %q", ct)
	}
}

func TestBodyTextFallback(t *testing.T) {
	p, h := bodyText(nil, nil, []byte("From: a@b.com\r\n\r\nhello body"))
	if p != "hello body" || h != "" {
		t.Fatalf("got %q %q", p, h)
	}
	p, _ = bodyText(nil, nil, []byte("From: a@b.com\n\nlf body"))
	if p != "lf body" {
		t.Fatalf("lf %q", p)
	}
	p, _ = bodyText(nil, nil, []byte("no separator"))
	if p != "" {
		t.Fatalf("want empty got %q", p)
	}
}

func TestFirstMessageIDVariants(t *testing.T) {
	if got := firstMessageID([]string{"  <a@b.com>  "}); got != "<a@b.com>" {
		t.Fatalf("got %q", got)
	}
	if got := firstMessageID([]string{"a@b.com c@d.com"}); !strings.Contains(got, "a@b.com") {
		t.Fatalf("got %q", got)
	}
	if got := firstMessageID([]string{"", "nope"}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := firstMessageID(nil); got != "" {
		t.Fatalf("nil %q", got)
	}
}

func TestOutboundFromEntityAndHeaders(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: mailer-daemon@example.com
Subject: Undeliverable
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="z"

--z
Content-Type: text/plain

x

--z
Content-Type: message/delivery-status

Final-Recipient: rfc822; a@example.com
Action: failed
Status: 5.1.1

--z
Content-Type: text/rfc822-headers

Message-ID: <inner@example.com>
X-EB-Outbound-Id: out_01jbf3k8v4w7q2m9x5c6t0r1yz

--z--
`, "\n", "\r\n"))
	entity, err := ebmime.ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	id := outboundFromEntity(entity)
	if id != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" {
		t.Fatalf("got %q", id)
	}
	if outboundFromEntity(nil) != "" {
		t.Fatal("nil entity")
	}
	hdr := ebmime.Header{"x-emailbridge-outbound-id": {"OUT_01jbf3k8v4w7q2m9x5c6t0r1yz"}}
	if outboundFromHeaders(hdr) != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" {
		t.Fatal("case fold")
	}
}

func TestExtractRecipientHintsBody(t *testing.T) {
	body := "The following addresses failed:\n  alice@example.com\nwas not delivered to bob@example.com\n"
	addrs := extractRecipientHints(body, nil)
	if len(addrs) < 1 {
		t.Fatalf("got %v", addrs)
	}
	hdr := ebmime.Header{"x-failed-recipients": {"carol@example.com, dave@example.com"}}
	addrs = extractRecipientHints("no markers", hdr)
	if len(addrs) != 2 {
		t.Fatalf("got %v", addrs)
	}
}

func TestParseDSNPerMessageOnlyAndDominant(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: md@example.com
Subject: bounce
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="q"

--q
Content-Type: text/plain

hi

--q
Content-Type: message/delivery-status

Final-Recipient: rfc822; mixed@example.com
Action: failed
Status: 5.1.1

Final-Recipient: rfc822; soft@example.com
Action: delayed
Status: 4.2.1

--q--
`, "\n", "\r\n"))
	entity, err := ebmime.ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := parseDSN(entity, ebmime.Header{}, "bounce")
	if r == nil || r.BounceType != BounceHard {
		t.Fatalf("got %+v", r)
	}
	if len(r.Recipients) != 2 {
		t.Fatalf("recs=%v", r.Recipients)
	}
}

func TestParseARFMinimal(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: a@example.com
MIME-Version: 1.0
Content-Type: multipart/report; report-type=feedback-report; boundary="f"

--f
Content-Type: text/plain

r

--f
Content-Type: message/feedback-report

Feedback-Type: fraud
Original-Mail-From: <sender@example.com>

--f--
`, "\n", "\r\n"))
	entity, err := ebmime.ParseEntity(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := parseARF(entity, ebmime.Header{})
	if r == nil || r.FeedbackType != "fraud" {
		t.Fatalf("got %+v", r)
	}
}

func TestDSNAddressAndTrunc(t *testing.T) {
	if dsnAddress("rfc822; <a@example.com>") != "a@example.com" {
		t.Fatal("dsnAddress")
	}
	if dsnAddress("") != "" {
		t.Fatal("empty")
	}
	long := strings.Repeat("x", 600)
	if len(trunc(long, 512)) != 512 {
		t.Fatal("trunc")
	}
	if trunc("short", 512) != "short" {
		t.Fatal("short")
	}
}

func TestJoinFromAndReadPartNil(t *testing.T) {
	if joinFrom([]ebmime.Address{{Name: "A", Address: "a@example.com"}, {Address: "b@example.com"}}) == "" {
		t.Fatal("join")
	}
	if readPart(nil, 10) != "" {
		t.Fatal("nil part")
	}
	e := &message.Entity{}
	if readPart(e, 10) != "" {
		t.Fatal("nil body")
	}
}

func TestHeuristicScoreTooLow(t *testing.T) {
	hdr := ebmime.Header{}
	if classifyHeuristicBounce(hdr, "hello", nil, "just a chat") != nil {
		t.Fatal("should be nil")
	}
	// subject alone + free text without status / sender is never a bounce
	if classifyHeuristicBounce(hdr, "Undelivered Mail", nil, "something about failure for a friend") != nil {
		t.Fatal("subject alone")
	}
	// daemon sender without body evidence is not enough
	from := []ebmime.Address{{Address: "mailer-daemon@mx.example.com"}}
	if classifyHeuristicBounce(hdr, "bounce", from, "something went wrong") != nil {
		t.Fatal("no body evidence")
	}
}

func TestClassifyAutoReplySubjectOnly(t *testing.T) {
	raw := []byte("From: a@example.com\r\nSubject: Out of Office: back Monday\r\n\r\nAway.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindAutoReply {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestParseVERPSimpleAndOutboundOnly(t *testing.T) {
	id, addr := parseVERP("out_01jbf3k8v4w7q2m9x5c6t0r1yz=bob=example.com")
	if id == "" || addr == "" {
		t.Fatalf("%s %s", id, addr)
	}
	id, addr = parseVERP("prefix-out_01jbf3k8v4w7q2m9x5c6t0r1yz-suffix")
	if id != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" || addr != "" {
		t.Fatalf("%s %s", id, addr)
	}
	id, addr = parseVERP("")
	if id != "" || addr != "" {
		t.Fatal("empty")
	}
}

func TestEnsureBlankLine(t *testing.T) {
	if !strings.HasSuffix(ensureBlankLine("From: a@b.com"), "\r\n\r\n") {
		t.Fatal("append")
	}
	s := "From: a@b.com\r\n\r\nbody"
	if ensureBlankLine(s) != s {
		t.Fatal("keep")
	}
}

func TestDominantBounceTypeSoftOnly(t *testing.T) {
	if dominantBounceType([]Recipient{{BounceType: BounceSoft}}) != BounceSoft {
		t.Fatal("soft")
	}
	if dominantBounceType(nil) != "" {
		t.Fatal("empty")
	}
}
