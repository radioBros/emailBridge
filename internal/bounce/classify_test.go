package bounce

import (
	"strings"
	"testing"
)

func TestClassifyEmpty(t *testing.T) {
	r, err := Classify(nil)
	if err != nil || r.Kind != KindNone {
		t.Fatalf("got %+v err=%v", r, err)
	}
	r, err = Classify([]byte("   "))
	if err != nil || r.Kind != KindNone {
		t.Fatalf("got %+v err=%v", r, err)
	}
}

func TestClassifyDSNHardAndCorrelation(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: Mail Delivery System <mailer-daemon@mx.example.com>
To: sender@example.com
Subject: Delivery Status Notification (Failure)
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="b1"

--b1
Content-Type: text/plain

fail

--b1
Content-Type: message/delivery-status

Reporting-MTA: dns; mx.example.com

Final-Recipient: rfc822; bob@example.com
Action: failed
Status: 5.1.1
Diagnostic-Code: smtp; 550 5.1.1 User unknown

--b1
Content-Type: message/rfc822

From: sender@example.com
To: bob@example.com
Subject: Hi
Message-ID: <corr-1@example.com>
X-EmailBridge-Outbound-Id: out_01jbf3k8v4w7q2m9x5c6t0r1yz

Hello

--b1--
`, "\n", "\r\n"))
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindDSNBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
	if r.OriginalMessageID != "<corr-1@example.com>" {
		t.Fatalf("msgid=%q", r.OriginalMessageID)
	}
	if r.OutboundID != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" {
		t.Fatalf("outbound=%q", r.OutboundID)
	}
	if len(r.Recipients) != 1 || r.Recipients[0].Address != "bob@example.com" {
		t.Fatalf("recipients=%v", r.Recipients)
	}
}

func TestClassifyDSNSoftQuota(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: MAILER-DAEMON@mx.example.com
Subject: Deferred
MIME-Version: 1.0
Content-Type: multipart/report; report-type=delivery-status; boundary="b2"

--b2
Content-Type: text/plain

quota

--b2
Content-Type: message/delivery-status

Final-Recipient: rfc822; full@example.it
Action: delayed
Status: 5.2.2
Diagnostic-Code: smtp; 552 mailbox full

--b2--
`, "\n", "\r\n"))
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindDSNBounce || r.BounceType != BounceSoft {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestClassifyARF(t *testing.T) {
	raw := []byte(strings.ReplaceAll(`From: abusedesk@isp.example.com
Subject: Abuse report
MIME-Version: 1.0
Content-Type: multipart/report; report-type=feedback-report; boundary="a1"

--a1
Content-Type: text/plain

report

--a1
Content-Type: message/feedback-report

Feedback-Type: abuse
Original-Message-ID: <arf-1@example.com>
Final-Recipient: rfc822; victim@example.com

--a1
Content-Type: text/rfc822-headers

From: sender@example.com
Message-ID: <arf-1@example.com>
X-EmailBridge-Outbound-Id: out_01jbf3k8v4w7q2m9x5c6t0r1yz

--a1--
`, "\n", "\r\n"))
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindComplaint || r.FeedbackType != "abuse" {
		t.Fatalf("got %+v", r)
	}
	if r.OutboundID != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" {
		t.Fatalf("outbound=%q", r.OutboundID)
	}
}

func TestClassifyAutoReply(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: Fuori ufficio\r\n" +
		"Auto-Submitted: auto-replied\r\n\r\nAway.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindAutoReply || !r.IsAutoReply {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyChallenge(t *testing.T) {
	raw := []byte("From: challenge@filter.example.com\r\nSubject: Please confirm your message\r\n" +
		"X-Challenge: abc\r\n\r\nYour message is held by a challenge-response system.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindChallengeResponse {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestClassifyHeuristicPostfix(t *testing.T) {
	raw := []byte("From: Mail Delivery System <mailer-daemon@mx.example.com>\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"X-Failed-Recipients: nobody@example.com\r\n\r\n" +
		"I'm sorry to have to inform you that your message could not\r\n" +
		"be delivered to one or more recipients.\r\n\r\n" +
		"<nobody@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestClassifyNone(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: Hello\r\n\r\nHi there.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestVERPExtraction(t *testing.T) {
	raw := []byte("From: mailer-daemon@mx.example.com\r\n" +
		"Subject: Undeliverable: test\r\n" +
		"Return-Path: <bounce+out_01jbf3k8v4w7q2m9x5c6t0r1yz=bob=example.com@mx.example.com>\r\n" +
		"X-Failed-Recipients: bob@example.com\r\n\r\n" +
		"Delivery failed permanently for the recipient.\r\n" +
		"<bob@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce {
		t.Fatalf("kind=%s", r.Kind)
	}
	if r.OutboundID != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" {
		t.Fatalf("outbound=%q", r.OutboundID)
	}
}

func TestHeuristicRejectsHumanSubjectCue(t *testing.T) {
	raw := []byte("From: alice@example.com\r\nSubject: Undelivered package tracking\r\n\r\n" +
		"Your parcel is on the way. Tracking 550 5.1.1 is not a bounce.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestHeuristicRejectsMailerDaemonSubstring(t *testing.T) {
	raw := []byte("From: support-mailer-daemon@shop.example.com\r\n" +
		"Subject: Failure notice about your order\r\n\r\n" +
		"User unknown is not relevant; this is a human support note.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestHeuristicRejectsQuotedBounceReply(t *testing.T) {
	raw := []byte("From: alice@example.com\r\nSubject: Re: Undelivered Mail Returned to Sender\r\n\r\n" +
		"Weird, look at this bounce I got:\r\n" +
		"> I'm sorry to have to inform you that your message could not\r\n" +
		"> <bob@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestListMessageNotAutoReply(t *testing.T) {
	raw := []byte("From: digest@lists.example.com\r\nSubject: Weekly digest — automatic reply tips\r\n" +
		"List-Id: <weekly.lists.example.com>\r\nPrecedence: list\r\n\r\n" +
		"This week's newsletter about out of office policies.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestBulkPrecedenceStillAutoReply(t *testing.T) {
	raw := []byte("From: a@example.com\r\nSubject: Abwesenheitsnotiz\r\n" +
		"Auto-Submitted: auto-replied\r\nPrecedence: bulk\r\n\r\n" +
		"Ich bin bis Montag nicht im Büro.\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindAutoReply {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestHeuristicDisplayNameNoReturnPath(t *testing.T) {
	raw := []byte("From: Mail Delivery System <noreply@mx.example.com>\r\n" +
		"Subject: Undelivered Mail Returned to Sender\r\n" +
		"X-Failed-Recipients: gone@example.com\r\n\r\n" +
		"I'm sorry to have to inform you that your message could not\r\n" +
		"be delivered to one or more recipients.\r\n\r\n" +
		"<gone@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestHeuristicTemplateStatusRecipientNoSender(t *testing.T) {
	raw := []byte("From: alerts@monitor.example.com\r\nSubject: notice\r\n\r\n" +
		"This is the mail system at host mx.example.com.\r\n" +
		"<gone@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce {
		t.Fatalf("got %s", r.Kind)
	}
}

func TestForwardedBounceIsNone(t *testing.T) {
	raw := []byte("From: alice@example.com\r\nSubject: FW: Undelivered Mail Returned to Sender\r\n\r\n" +
		"Look at this bounce:\r\n" +
		"---------- Forwarded message ---------\r\n" +
		"From: Mail Delivery System <mailer-daemon@mx.example.com>\r\n" +
		"\r\n" +
		"This is the mail system at host mx.example.com.\r\n" +
		"<gone@example.com>: host mx said: 550 5.1.1 User unknown\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindNone {
		t.Fatalf("forwarded bounce got %s", r.Kind)
	}
}

func TestExchangeOutlookDaemonBounce(t *testing.T) {
	raw := []byte("From: Microsoft Outlook <MicrosoftExchange329E594659@example.com>\r\n" +
		"To: sender@example.com\r\nSubject: Undeliverable: Hello\r\n" +
		"Auto-Submitted: auto-generated\r\n\r\n" +
		"Delivery has failed to these recipients or groups:\r\n" +
		"\r\n" +
		"gone@example.com\r\n" +
		"\r\n" +
		"Remote Server returned '550 5.1.1 RESOLVER.ADR.RecipNotFound; not found'\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
	if len(r.FinalRecipients) == 0 || r.FinalRecipients[0] != "gone@example.com" {
		t.Fatalf("recipients=%v", r.FinalRecipients)
	}
}

func TestPolicySpamRejectHard(t *testing.T) {
	raw := []byte("From: mailer-daemon@mx.example.com\r\nSubject: Returned mail\r\n\r\n" +
		"This is the mail system at host mx.example.com.\r\n" +
		"Recipient: <spam@example.com>\r\n" +
		"550 5.7.1 Message rejected as spam / blocked by policy\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestItalianProviderBounce(t *testing.T) {
	raw := []byte("From: Sistema di posta <postmaster@aruba.it>\r\n" +
		"Subject: Notifica di mancato recapito\r\n" +
		"X-Failed-Recipients: gone@example.it\r\n\r\n" +
		"Messaggio non recapitato.\r\n" +
		"Impossibile recapitare il messaggio a gone@example.it\r\n" + //nolint:misspell // IT
		"Errore di consegna: 550 5.1.1 destinatario inesistente\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceHard {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestChallengeBoxbeAndSpamgourmet(t *testing.T) {
	boxbe := []byte("From: notify@boxbe.com\r\nSubject: Please approve this sender\r\n\r\n" +
		"Boxbe is holding a message until you add them to your guest list.\r\n")
	r, err := Classify(boxbe)
	if err != nil || r.Kind != KindChallengeResponse {
		t.Fatalf("boxbe got %+v err=%v", r, err)
	}
	sg := []byte("From: watch@spamgourmet.com\r\nSubject: Remaining stamps exhausted\r\n\r\n" +
		"This message was discarded by spamgourmet because you have 0 remaining stamps.\r\n")
	r, err = Classify(sg)
	if err != nil || r.Kind != KindChallengeResponse {
		t.Fatalf("spamgourmet got %+v err=%v", r, err)
	}
}

func TestHeuristicNullReturnPathSoft(t *testing.T) {
	raw := []byte("From: MAILER-DAEMON@mx.example.com\r\n" +
		"Return-Path: <>\r\nSubject: Delayed Mail\r\n" +
		"X-Failed-Recipients: full@example.com\r\n\r\n" +
		"Delivery temporarily deferred for full@example.com.\r\n" +
		"451 4.2.1 try again later — mailbox resources temporarily unavailable\r\n")
	r, err := Classify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHeuristicBounce || r.BounceType != BounceSoft {
		t.Fatalf("got kind=%s bt=%s", r.Kind, r.BounceType)
	}
}

func TestBounceTypeFromStatus(t *testing.T) {
	cases := []struct {
		status, diag string
		want         BounceType
	}{
		{"4.4.1", "", BounceSoft},
		{"5.1.1", "", BounceHard},
		{"5.2.2", "mailbox full", BounceSoft},
		{"5.2.2", "", BounceSoft},
		{"", "550 user unknown", BounceHard},
		{"", "451 try again", BounceSoft},
	}
	for _, c := range cases {
		got := bounceTypeFromStatus(c.status, c.diag, "failed")
		if got != c.want {
			t.Fatalf("status=%q diag=%q: got %s want %s", c.status, c.diag, got, c.want)
		}
	}
}

func TestParseVERP(t *testing.T) {
	id, addr := parseVERP("bounce+out_01jbf3k8v4w7q2m9x5c6t0r1yz=bob=example.com@mx.example.com")
	if id != "out_01jbf3k8v4w7q2m9x5c6t0r1yz" || addr != "bob@example.com" {
		t.Fatalf("got %s %s", id, addr)
	}
}
