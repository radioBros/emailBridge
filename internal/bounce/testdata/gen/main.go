//go:build ignore

// Generates synthetic bounce/ARF/auto-reply corpus under ../corpus.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type expected struct {
	Kind       string `json:"kind"`
	BounceType string `json:"bounceType,omitempty"`
}

func main() {
	// Resolve testdata/corpus relative to this source file's directory.
	srcDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	root := filepath.Join(srcDir, "testdata", "corpus")
	// When run as `go run ./testdata/gen` from internal/bounce, cwd is the package dir.
	if _, err := os.Stat(filepath.Join(srcDir, "testdata", "gen")); err != nil {
		root = filepath.Join(srcDir, "corpus")
	}
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		panic(err)
	}
	n := 0
	write := func(name, kind, bt, eml string) {
		n++
		base := filepath.Join(root, name)
		if err := os.WriteFile(base+".eml", []byte(eml), 0o644); err != nil {
			panic(err)
		}
		b, _ := json.Marshal(expected{Kind: kind, BounceType: bt})
		if err := os.WriteFile(base+".json", append(b, '\n'), 0o644); err != nil {
			panic(err)
		}
	}

	boundary := func(i int) string { return fmt.Sprintf("ebbound%03d", i) }

	// --- DSN hard (25) ---
	for i := 1; i <= 25; i++ {
		b := boundary(i)
		status := []string{"5.1.1", "5.1.2", "5.1.3", "5.4.1", "5.7.1"}[(i-1)%5]
		addr := fmt.Sprintf("user%02d@example.com", i)
		mid := fmt.Sprintf("<orig-dsn-hard-%03d@example.com>", i)
		oid := fmt.Sprintf("out_01jbf3k8v4w7q2m9x5c6t0r%02d", i%100)
		write(fmt.Sprintf("dsn_hard_%03d", i), "dsn_bounce", "hard", dsnMail(b, addr, status, "failed",
			fmt.Sprintf("smtp; 550 %s User unknown", status), mid, oid, "hard"))
	}

	// --- DSN soft (20) ---
	for i := 1; i <= 20; i++ {
		b := boundary(100 + i)
		status := []string{"4.2.1", "4.3.2", "4.4.1", "4.7.1", "5.2.2"}[(i-1)%5]
		addr := fmt.Sprintf("temp%02d@example.it", i)
		mid := fmt.Sprintf("<orig-dsn-soft-%03d@example.com>", i)
		diag := "smtp; 451 Try again later"
		if status == "5.2.2" {
			diag = "smtp; 552 5.2.2 Mailbox full"
		}
		write(fmt.Sprintf("dsn_soft_%03d", i), "dsn_bounce", "soft", dsnMail(b, addr, status, "delayed",
			diag, mid, "", "soft"))
	}

	// --- ARF complaints (15) ---
	for i := 1; i <= 15; i++ {
		b := boundary(200 + i)
		ft := []string{"abuse", "fraud", "virus", "other", "not-spam"}[(i-1)%5]
		mid := fmt.Sprintf("<orig-arf-%03d@example.com>", i)
		oid := fmt.Sprintf("out_01jbf3k8v4w7q2m9x5c6t1r%02d", i)
		write(fmt.Sprintf("arf_%03d", i), "complaint", "", arfMail(b, ft, mid, oid, fmt.Sprintf("victim%02d@example.com", i)))
	}

	// --- Auto-reply EN/IT/DE/FR/ES (25) ---
	oos := []struct{ subj, lang string }{
		{"Out of Office: away until Monday", "en"},
		{"Automatic reply: I am on vacation", "en"},
		{"Auto-Reply: currently away", "en"},
		{"Away from office", "en"},
		{"On holiday — limited email", "en"},
		{"Fuori ufficio fino al 30", "it"},
		{"Risposta automatica: sono assente", "it"},
		{"In ferie — ritorno il 5", "it"},
		{"Non in ufficio", "it"},
		{"Assenza programmata", "it"},
		{"Abwesenheitsnotiz", "de"},
		{"Automatische Antwort: ich bin abwesend", "de"},
		{"Urlaub — zurück am Montag", "de"},
		{"Out of Office AutoReply", "de"},
		{"Ich bin abwesend", "de"},
		{"Réponse automatique: je suis absent", "fr"},
		{"Absence du bureau", "fr"},
		{"Congés jusqu'au 12", "fr"},
		{"Reponse automatique", "fr"},
		{"Je suis absent du bureau", "fr"},
		{"Respuesta automática: estoy ausente", "es"},
		{"Fuera de la oficina", "es"},
		{"Vacaciones — vuelvo el lunes", "es"},
		{"Ausencia temporal", "es"},
		{"Estoy ausente hasta el viernes", "es"},
	}
	for i, o := range oos {
		hdr := "Auto-Submitted: auto-replied\r\n"
		if i%3 == 1 {
			hdr = "X-Autoreply: yes\r\nPrecedence: bulk\r\n"
		}
		if i%3 == 2 {
			hdr = "Auto-Submitted: auto-generated\r\nX-Auto-Response-Suppress: All\r\n"
		}
		write(fmt.Sprintf("autoreply_%03d", i+1), "auto_reply", "", fmt.Sprintf(
			"From: agent%02d@example.com\r\nTo: you@example.com\r\nSubject: %s\r\n"+
				"Message-ID: <oof-%03d@example.com>\r\nIn-Reply-To: <orig-oof-%03d@example.com>\r\n"+
				"%sMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
				"I am away from the office (%s). Your message will be read later.\r\n",
			i+1, o.subj, i+1, i+1, hdr, o.lang))
	}

	// --- Challenge-response (10) ---
	for i := 1; i <= 10; i++ {
		write(fmt.Sprintf("challenge_%03d", i), "challenge_response", "", fmt.Sprintf(
			"From: challenge@filter.example.com\r\nTo: you@example.com\r\n"+
				"Subject: Confirmation required — challenge %d\r\n"+
				"X-Challenge: token-%d\r\nMessage-ID: <ch-%03d@example.com>\r\n"+
				"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
				"Your message is being held by a challenge-response spam filter.\r\n"+
				"Please confirm by sending a blank reply.\r\n", i, i, i))
	}

	// --- Heuristic MTA bounces (40) ---
	mtas := []struct {
		name, from, subj, bodyHard, bodySoft string
	}{
		{"postfix", "Mail Delivery System <mailer-daemon@mx.example.com>",
			"Undelivered Mail Returned to Sender",
			"I'm sorry to have to inform you that your message could not\nbe delivered to one or more recipients.\n\n<fail%02d@example.com>: host mx.example.com said:\n550 5.1.1 User unknown (in reply to RCPT TO command)",
			"I'm sorry to have to inform you that your message could not\nbe delivered to one or more recipients.\n\n<full%02d@example.com>: host mx.example.com said:\n452 4.2.2 Mailbox full"},
		{"exim", "Mail Delivery System <mailer-daemon@exim.example.com>",
			"Mail delivery failed: returning message to sender",
			"This message was created automatically by mail delivery software.\n\nA message that you sent could not be delivered to one or more of its\nrecipients. This is a permanent error.\n\nfail%02d@example.com\n  SMTP error from remote mail server after RCPT TO:\n  host mx.example.com: 550 5.1.1 unknown user",
			"This message was created automatically by mail delivery software.\nTemporary failure while talking to remote host.\nfull%02d@example.it\n  451 4.3.2 Try again later"},
		{"sendmail", "Mailer-Daemon <mailer-daemon@sendmail.example.com>",
			"Returned mail: User unknown",
			"The original message was received at Mon, 1 Jan 2024\n----- The following addresses had permanent fatal errors -----\n<fail%02d@example.com>\n(reason: 550 5.1.1 User unknown)\n\n-- Diagnostic-Code: SMTP; 550 5.1.1 User unknown",
			"The original message was received at Mon, 1 Jan 2024\n----- Transient non-fatal errors -----\n<full%02d@example.com>\n(reason: 421 4.4.1 Connection timed out)"},
		{"exchange", "Microsoft Outlook <postmaster@exchange.example.com>",
			"Undeliverable: Your message",
			"Your message to fail%02d@example.com couldn't be delivered.\nRemote server returned '550 5.1.1 RESOLVER.ADR.RecipNotFound; not found'",
			"Your message to full%02d@example.com couldn't be delivered.\nRemote server returned '452 4.2.2 mailbox full / over quota'"},
		{"gmail", "Mail Delivery Subsystem <mailer-daemon@google.com>",
			"Delivery Status Notification (Failure)",
			"Delivery to the following recipient failed permanently:\n\nfail%02d@example.com\n\nTechnical details:\n550-5.1.1 The email account that you tried to reach does not exist.",
			"Delivery to the following recipient was delayed:\n\nfull%02d@example.com\n\nTechnical details of temporary failure:\n451 4.3.0 Mail server temporarily rejected message."},
		{"yahoo", "Mailer-Daemon <mailer-daemon@yahoo.com>",
			"Failure Delivery",
			"Sorry, we were unable to deliver your message to the following address.\n\n<fail%02d@example.com>:\n550 5.1.1 User doesn't exist",
			"Sorry, we were unable to deliver your message to the following address.\n\n<full%02d@example.com>:\n451 4.7.1 Greylisted, please try again later"},
		{"qmail", "Mailer-Daemon <mailer-daemon@qmail.example.com>",
			"failure notice",
			"Hi. This is the qmail-send program at mx.example.com.\nI'm afraid I wasn't able to deliver your message to the following addresses.\n\n<fail%02d@example.com>:\n550 sorry, no mailbox here by that name (#5.1.1)",
			"Hi. This is the qmail-send program at mx.example.com.\nDeferred:\n<full%02d@example.com>:\n451 Unable to accept message temporarily"},
		{"aruba", "Mail Delivery System <postmaster@aruba.it>",
			"Messaggio non consegnato — indirizzo inesistente",
			"Impossibile consegnare il messaggio a fail%02d@example.it\nErrore di consegna permanente: 550 5.1.1 destinatario sconosciuto\n(smtps.aruba.it)",
			"Impossibile consegnare il messaggio a full%02d@example.it\nCasella piena / quota: 452 4.2.2 mailbox full"},
		{"register", "Postmaster <postmaster@register.it>",
			"Errore di consegna: non recapitabile",
			"Il messaggio non e' recapitabile a fail%02d@example.it\n550 5.1.1 user unknown — permanent failure",
			"Il messaggio non e' recapitabile a full%02d@example.it\n451 temporary failure — riprovare"},
		{"libero", "Mailer-Daemon <mailer-daemon@libero.it>",
			"Non recapitabile: utente sconosciuto",
			"Delivery error from mail.libero.it\nfail%02d@example.it rejected: 550 5.1.1 unknown user",
			"Delivery error from mail.libero.it\nfull%02d@example.it: 421 resources temporarily unavailable"},
	}
	hi, si := 0, 0
	for _, m := range mtas {
		for j := 0; j < 2; j++ {
			hi++
			body := fmt.Sprintf(m.bodyHard, hi)
			write(fmt.Sprintf("heur_%s_hard_%02d", m.name, j+1), "heuristic_bounce", "hard",
				plainBounce(m.from, m.subj, body, hi))
		}
		for j := 0; j < 2; j++ {
			si++
			body := fmt.Sprintf(m.bodySoft, si)
			write(fmt.Sprintf("heur_%s_soft_%02d", m.name, j+1), "heuristic_bounce", "soft",
				plainBounce(m.from, m.subj+" (deferred)", body, si+50))
		}
	}

	// --- Normal mail (none) (20) ---
	for i := 1; i <= 20; i++ {
		write(fmt.Sprintf("none_%03d", i), "none", "", fmt.Sprintf(
			"From: friend%02d@example.com\r\nTo: you@example.com\r\n"+
				"Subject: Lunch plans %d\r\nMessage-ID: <none-%03d@example.com>\r\n"+
				"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
				"Hi, shall we meet at noon? Regards.\r\n", i, i, i))
	}

	fmt.Printf("wrote %d corpus samples to %s\n", n, root)
}

func dsnMail(boundary, addr, status, action, diag, origMID, outboundID, _ string) string {
	var xb strings.Builder
	xb.WriteString(fmt.Sprintf(
		"From: Mail Delivery System <mailer-daemon@mx.example.com>\r\n"+
			"To: sender@example.com\r\n"+
			"Subject: Delivery Status Notification (Failure)\r\n"+
			"Message-ID: <dsn-%s@example.com>\r\n"+
			"Auto-Submitted: auto-replied\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: multipart/report; report-type=delivery-status;\r\n boundary=\"%s\"\r\n\r\n"+
			"--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
			"Your message could not be delivered to %s.\r\n\r\n"+
			"--%s\r\nContent-Type: message/delivery-status\r\n\r\n"+
			"Reporting-MTA: dns; mx.example.com\r\n\r\n"+
			"Final-Recipient: rfc822; %s\r\n"+
			"Action: %s\r\n"+
			"Status: %s\r\n"+
			"Diagnostic-Code: %s\r\n\r\n"+
			"--%s\r\nContent-Type: message/rfc822\r\n\r\n"+
			"From: sender@example.com\r\nTo: %s\r\n"+
			"Subject: Hello\r\nMessage-ID: %s\r\n",
		boundary, boundary, boundary, addr, boundary, addr, action, status, diag, boundary, addr, origMID))
	if outboundID != "" {
		xb.WriteString(fmt.Sprintf("X-EmailBridge-Outbound-Id: %s\r\n", outboundID))
	}
	xb.WriteString("Content-Type: text/plain\r\n\r\nHello world.\r\n")
	xb.WriteString(fmt.Sprintf("\r\n--%s--\r\n", boundary))
	return xb.String()
}

func arfMail(boundary, ft, origMID, outboundID, victim string) string {
	return fmt.Sprintf(
		"From: <abusedesk@isp.example.com>\r\nTo: <complaint@example.com>\r\n"+
			"Subject: FW: Abuse report\r\nMIME-Version: 1.0\r\n"+
			"Content-Type: multipart/report; report-type=feedback-report;\r\n boundary=\"%s\"\r\n\r\n"+
			"--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
			"This is an email abuse report.\r\n\r\n"+
			"--%s\r\nContent-Type: message/feedback-report\r\n\r\n"+
			"Feedback-Type: %s\r\nUser-Agent: FakeARF/1.0\r\n"+
			"Version: 1\r\nOriginal-Mail-From: <sender@example.com>\r\n"+
			"Original-Message-ID: %s\r\nFinal-Recipient: rfc822; %s\r\n\r\n"+
			"--%s\r\nContent-Type: message/rfc822\r\n\r\n"+
			"From: sender@example.com\r\nTo: %s\r\nSubject: Offer\r\n"+
			"Message-ID: %s\r\nX-EmailBridge-Outbound-Id: %s\r\n\r\nBuy now.\r\n"+
			"\r\n--%s--\r\n",
		boundary, boundary, boundary, ft, origMID, victim, boundary, victim, origMID, outboundID, boundary)
}

func plainBounce(from, subj, body string, n int) string {
	return fmt.Sprintf(
		"From: %s\r\nTo: sender@example.com\r\nSubject: %s\r\n"+
			"Message-ID: <heur-%03d@example.com>\r\n"+
			"In-Reply-To: <orig-heur-%03d@example.com>\r\n"+
			"X-Failed-Recipients: bounce-hint%03d@example.com\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, subj, n, n, n, body)
}
