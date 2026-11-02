package bounce

import (
	"net/mail"
	"regexp"
	"strings"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

var (
	reStatusLine = regexp.MustCompile(`(?i)\b([45]\d\d)\s+([45]\.\d+\.\d+)\b`)
	reEnhanced   = regexp.MustCompile(`\b([45]\.\d+\.\d+)\b`)
	reSMTPCode   = regexp.MustCompile(`(?i)\b([45]\d\d)\b`)
	reEmail      = regexp.MustCompile(`(?i)\b([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})\b`)
)

// Known MTA bounce template phrases (EN + common locale variants).
var mtaTemplates = []string{
	// Postfix
	"this is the mail system at host",
	"i'm sorry to have to inform you that your message could not",
	"undelivered mail returned to sender",
	// Exim
	"this message was created automatically by mail delivery software",
	// qmail
	"this is the qmail-send program",
	"hi. this is the qmail",
	// Sendmail
	"the original message was received at",
	"mail delivery subsystem",
	// Exchange / O365
	"delivery has failed to these recipients",
	"delivery has failed to these recipients or groups",
	"remote server returned",
	"your message to",
	// Gmail
	"delivery to the following recipient failed",
	"delivery to the following recipient failed permanently",
	"address not found",
	// Yahoo
	"sorry, we were unable to deliver your message",
	"sorry, we were unable to deliver",
	// Policy / spam
	"rejected as spam",
	"content rejected",
	"blocked by policy",
	"message rejected",
	// Generic / Italian / DE / FR / ES
	"the following address(es) failed",
	"the following recipients failed",
	"permanent failure",
	"temporary failure",
	"impossibile consegnare",              //nolint:misspell // IT
	"impossibile recapitare il messaggio", //nolint:misspell // IT
	"messaggio non recapitato",
	"messaggio non consegnato",
	"il messaggio non è stato consegnato",
	"il messaggio non e stato consegnato",
	"errore di consegna",
	"notifica di mancato recapito",
	"destinatario sconosciuto",
	"destinatario inesistente",
	"non recapitabile",
	"sistema di posta",
	"casella piena",
	"zustellung fehlgeschlagen",
	"unzustellbar",
	"diese nachricht konnte nicht zugestellt werden",
	"échec de livraison",
	"echec de livraison",
	"non remis",
	"no se pudo entregar",
	"entrega fallida",
	"wasn't able to deliver your message",
	"could not be delivered",
	"couldn't be delivered",
	"unable to deliver your message",
	"resources temporarily unavailable",
	"try again later",
	"greylisted",
	"mailbox full",
	"over quota",
}

// classifyHeuristicBounce needs two independent kinds of evidence (never subject alone):
//
//	sender evidence + body evidence, OR template phrase + SMTP status + recipient.
func classifyHeuristicBounce(hdr ebmime.Header, subject string, from []ebmime.Address, body string) *Result {
	// Sender evidence is outer-header only (From / Return-Path / Auto-Submitted / X-Failed-Recipients).
	senderOK := hasSenderEvidence(hdr, from)
	// Human forward/reply of a bounce (FW:/Fwd:/… or forwarded-message markers) is never a bounce.
	if !senderOK && isHumanForwardOrReply(subject, body) {
		return nil
	}

	template := hasMTATemplate(body)
	status, diag := extractStatusDiag(body)
	hasStatus := status != "" || diag != "" || hasSMTPStatus(body)
	addrs := extractRecipientHints(body, hdr)
	hasRecipient := len(addrs) > 0
	bodyOK := template || hasSMTPNearRecipient(body, hdr) || hasQuotedHeaderBlock(body)

	pathSenderBody := senderOK && bodyOK
	// Triple path only when the outer sender is not a human forward/reply of quoted bounce text.
	pathTriple := template && hasStatus && hasRecipient && !isHumanForwardOrReply(subject, body)
	if !pathSenderBody && !pathTriple {
		return nil
	}

	bt := bounceTypeFromSubjectBody(subject, body)
	if bt == "" {
		bt = bounceTypeFromStatus(status, diag, "")
	}
	if bt == "" && hasStatus {
		if m := reSMTPCode.FindStringSubmatch(body); len(m) == 2 {
			if strings.HasPrefix(m[1], "4") {
				bt = BounceSoft
			} else if strings.HasPrefix(m[1], "5") {
				bt = BounceHard
			}
		}
	}
	if bt == "" {
		bt = BounceHard
	}
	recs := make([]Recipient, 0, len(addrs))
	if len(addrs) == 0 {
		recs = append(recs, Recipient{
			Address: "", Action: "failed", Status: status, Diagnostic: trunc(diag, 512), BounceType: bt,
		})
	} else {
		for _, a := range addrs {
			recs = append(recs, Recipient{
				Address: a, Action: "failed", Status: status, Diagnostic: trunc(diag, 512), BounceType: bt,
			})
		}
	}
	return &Result{
		Kind:            KindHeuristicBounce,
		BounceType:      bt,
		Recipients:      recs,
		FinalRecipients: addrs,
		MTA:             guessMTA(hdr, subject, body),
		Confidence:      0.9,
	}
}

func hasSenderEvidence(hdr ebmime.Header, from []ebmime.Address) bool {
	if isNullReturnPath(hdr) {
		return true
	}
	if strings.TrimSpace(headerGet(hdr, "x-failed-recipients")) != "" {
		return true
	}
	as := strings.ToLower(strings.TrimSpace(headerGet(hdr, "auto-submitted")))
	if as == "auto-generated" || as == "auto-replied" {
		return true
	}
	return isDaemonSender(from)
}

func isNullReturnPath(hdr ebmime.Header) bool {
	v := strings.TrimSpace(headerGet(hdr, "return-path"))
	return v == "<>" || strings.EqualFold(v, "< >")
}

func isDaemonSender(from []ebmime.Address) bool {
	for _, a := range from {
		if isDaemonLocal(localPart(a.Address)) {
			return true
		}
		if isDaemonDisplayName(a.Name) {
			return true
		}
	}
	return false
}

func isDaemonLocal(local string) bool {
	local = strings.ToLower(strings.TrimSpace(local))
	if local == "" {
		return false
	}
	// Sub-address: mailer-daemon+tag
	if i := strings.IndexByte(local, '+'); i > 0 {
		local = local[:i]
	}
	switch local {
	case "mailer-daemon", "mailerdaemon", "postmaster", "mail-delivery-subsystem",
		"double-bounce", "bounce", "bounces":
		return true
	}
	// mailer-daemon-… extension forms (not *mailer-daemon as a suffix of another word)
	if strings.HasPrefix(local, "mailer-daemon-") || strings.HasPrefix(local, "mailerdaemon-") {
		return true
	}
	// Exchange/Outlook online: MicrosoftExchange329E…@… / MicrosoftOutlook…
	if strings.HasPrefix(local, "microsoftexchange") || strings.HasPrefix(local, "microsoftoutlook") {
		return true
	}
	return false
}

func isDaemonDisplayName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	names := []string{
		"mail delivery system", "mail delivery subsystem", "mailer-daemon", "mailerdaemon",
		"postmaster", "sistema di posta", "mail delivery",
		"microsoft exchange", "microsoft outlook",
	}
	for _, d := range names {
		if n == d || strings.Contains(n, d) {
			return true
		}
	}
	return false
}

func localPart(addr string) string {
	addr = strings.Trim(strings.TrimSpace(addr), "<>")
	if addr == "" {
		return ""
	}
	if at := strings.LastIndex(addr, "@"); at > 0 {
		return addr[:at]
	}
	if parsed, err := mail.ParseAddress(addr); err == nil && parsed.Address != "" {
		a := parsed.Address
		if at := strings.LastIndex(a, "@"); at > 0 {
			return a[:at]
		}
	}
	return addr
}

func hasMTATemplate(body string) bool {
	bodyL := strings.ToLower(body)
	for _, t := range mtaTemplates {
		if strings.Contains(bodyL, t) {
			return true
		}
	}
	return false
}

func hasSMTPStatus(body string) bool {
	if reStatusLine.MatchString(body) || reEnhanced.MatchString(body) {
		return true
	}
	return reSMTPCode.MatchString(body)
}

func hasSMTPNearRecipient(body string, hdr ebmime.Header) bool {
	if !hasSMTPStatus(body) {
		return false
	}
	if len(extractRecipientHints(body, hdr)) > 0 {
		return true
	}
	// Same-line email + SMTP code
	for _, ln := range strings.Split(body, "\n") {
		if reEmail.MatchString(ln) && reSMTPCode.MatchString(ln) {
			return true
		}
	}
	return false
}

func isHumanForwardOrReply(subject, body string) bool {
	s := strings.ToLower(strings.TrimSpace(subject))
	if isReplySubject(s) || isForwardSubject(s) {
		return true
	}
	return hasForwardBodyMarker(body)
}

func isHumanReplyWithQuote(subject, body string) bool {
	s := strings.ToLower(strings.TrimSpace(subject))
	if !isReplySubject(s) {
		return false
	}
	return hasQuotedReplyBody(body)
}

func isReplySubject(s string) bool {
	for _, p := range []string{"re:", "r:", "aw:", "sv:", "vs:", "rif:", "ris:", "antw:", "odp:"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isForwardSubject(s string) bool {
	// EN FW:/Fwd:, IT I:, DE WG:, FR TR: (and common variants). Prefix only.
	for _, p := range []string{"fw:", "fwd:", "fwd :", "i:", "wg:", "tr:", "tlv:", "enc:", "vl:"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func hasForwardBodyMarker(body string) bool {
	lower := strings.ToLower(body)
	markers := []string{
		"-----original message-----",
		"----- original message -----",
		"---------- forwarded message ---------",
		"--------- forwarded message ---------",
		"begin forwarded message",
		"messaggio originale",
		"inizio messaggio inoltrato",
		"weitergeleitete nachricht",
		"message transféré",
		"message transfere", //nolint:misspell // FR unaccented
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func hasQuotedReplyBody(body string) bool {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, ">") {
			return true
		}
		tl := strings.ToLower(t)
		if strings.HasPrefix(tl, "on ") && strings.Contains(tl, " wrote:") {
			return true
		}
		if strings.HasPrefix(tl, "il ") && strings.Contains(tl, "ha scritto:") {
			return true
		}
	}
	return false
}

func hasQuotedHeaderBlock(body string) bool {
	// Forward markers are handled separately (human forward → none). Here: bounce-embedded originals.
	lower := strings.ToLower(body)
	markers := []string{
		"--- below this line is a copy",
		"the original message was received",
		"content-type: message/rfc822",
		"content-type: text/rfc822-headers",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	var hdrLines int
	hasMID := false
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			if hdrLines >= 2 && hasMID {
				return true
			}
			hdrLines = 0
			hasMID = false
			continue
		}
		if i := strings.IndexByte(t, ':'); i > 0 && i < 40 && !strings.Contains(t[:i], " ") {
			name := strings.ToLower(t[:i])
			hdrLines++
			if name == "message-id" || name == "return-path" || name == "from" {
				hasMID = true
			}
			continue
		}
		if hdrLines >= 2 && hasMID {
			return true
		}
		hdrLines = 0
		hasMID = false
	}
	return hdrLines >= 2 && hasMID
}

func joinFrom(from []ebmime.Address) string {
	var b strings.Builder
	for i, a := range from {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(a.Name)
		b.WriteByte(' ')
		b.WriteString(a.Address)
	}
	return b.String()
}

func looksLikeBounceSubject(s string) bool {
	cues := []string{
		"undeliverable", "undelivered", "delivery status notification",
		"delivery failure", "delivery failed", "mail delivery failed",
		"returned mail", "returned to sender", "mail system error",
		"failure notice", "failure delivery", "delivery error",
		"non-delivery", "nondelivery", "not delivered",
		"message not delivered", "could not be delivered",
		"unable to deliver", "delivery notification: delivery has failed",
		"mail delivery subsystem",
		"non recapitabile", "non recapitabili", "messaggio non consegnato",
		"errore di consegna", "indirizzo inesistente", "destinatario sconosciuto",
		"impossibile consegnare", //nolint:misspell // IT
		"unzustellbar", "zustellung fehlgeschlagen",
		"non remis", "échec de livraison", "echec de livraison", "retour à l'expéditeur",
		"no se pudo entregar", "entrega fallida", "correo no entregado",
		"undeliverable: ", "undeliverable mail",
	}
	for _, c := range cues {
		if strings.Contains(s, c) {
			return true
		}
	}
	return false
}

func bounceTypeFromSubjectBody(subject, body string) BounceType {
	text := strings.ToLower(subject + "\n" + body)
	soft := []string{
		"mailbox full", "over quota", "quota exceeded", "try again", "temporary",
		"temporarily", "greylist", "graylist", "greylisted", "resources temporarily",
		"451 ", "450 ", "421 ", "452 ", "4.2.2", "4.3.2", "4.4.1", "4.7.1",
		"casella piena", "riprovare", "deferred",
	}
	hard := []string{
		"user unknown", "unknown user", "does not exist", "doesn't exist", "invalid recipient",
		"recipient rejected", "no such user", "mailbox unavailable",
		"address rejected", "550 ", "551 ", "553 ", "554 ", "5.1.1", "5.1.2", "5.1.3",
		"5.4.1", "5.7.1", "5.7.0", "destinatario sconosciuto", "destinatario inesistente", "inesistente",
		"permanent failure", "permanently rejected", "spam", "rejected as spam",
		"content rejected", "blocked by policy", "message rejected",
	}
	for _, s := range soft {
		if strings.Contains(text, s) {
			return BounceSoft
		}
	}
	for _, h := range hard {
		if strings.Contains(text, h) {
			return BounceHard
		}
	}
	if m := reStatusLine.FindStringSubmatch(text); len(m) == 3 {
		return bounceTypeFromStatus(m[2], m[0], "")
	}
	if m := reEnhanced.FindStringSubmatch(text); len(m) == 2 {
		return bounceTypeFromStatus(m[1], "", "")
	}
	if m := reSMTPCode.FindStringSubmatch(text); len(m) == 2 {
		if strings.HasPrefix(m[1], "4") {
			return BounceSoft
		}
		if strings.HasPrefix(m[1], "5") {
			return BounceHard
		}
	}
	return ""
}

func extractStatusDiag(body string) (status, diag string) {
	if m := reStatusLine.FindStringSubmatch(body); len(m) == 3 {
		return m[2], trunc(strings.TrimSpace(m[0]), 512)
	}
	if m := reEnhanced.FindStringSubmatch(body); len(m) == 2 {
		status = m[1]
	}
	for _, ln := range strings.Split(body, "\n") {
		l := strings.ToLower(ln)
		trim := strings.TrimSpace(l)
		if strings.Contains(l, "remote host said") || strings.Contains(l, "remote server returned") ||
			strings.Contains(l, "diagnostic") ||
			strings.HasPrefix(trim, "550") || strings.HasPrefix(trim, "551") ||
			strings.HasPrefix(trim, "553") || strings.HasPrefix(trim, "554") ||
			strings.HasPrefix(trim, "451") || strings.HasPrefix(trim, "450") ||
			strings.HasPrefix(trim, "421") || strings.HasPrefix(trim, "452") {
			diag = trunc(strings.TrimSpace(ln), 512)
			if status == "" {
				if m := reStatusLine.FindStringSubmatch(ln); len(m) == 3 {
					status = m[2]
				} else if m := reEnhanced.FindStringSubmatch(ln); len(m) == 2 {
					status = m[1]
				}
			}
			break
		}
	}
	if status == "" {
		if m := reSMTPCode.FindStringSubmatch(body); len(m) == 2 {
			// Prefer leaving status as enhanced when possible; SMTP code goes to diag.
			if diag == "" {
				diag = m[1]
			}
		}
	}
	return status, diag
}

func extractRecipientHints(body string, hdr ebmime.Header) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(a string) {
		a = strings.ToLower(strings.Trim(a, "<> \t\"'"))
		if a == "" || !strings.Contains(a, "@") {
			return
		}
		local := localPart(a)
		if isDaemonLocal(local) {
			return
		}
		if strings.HasPrefix(local, "microsoftexchange") || strings.HasPrefix(local, "microsoftoutlook") {
			return
		}
		if _, ok := seen[a]; ok {
			return
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	lines := strings.Split(body, "\n")
	inExchangeBlock := false
	for _, line := range lines {
		l := strings.ToLower(line)
		t := strings.TrimSpace(line)
		if strings.Contains(l, "delivery has failed to these recipients") {
			inExchangeBlock = true
			continue
		}
		if inExchangeBlock {
			if strings.Contains(l, "remote server returned") || strings.Contains(l, "generating server") ||
				strings.HasPrefix(strings.ToLower(t), "sent by") {
				inExchangeBlock = false
			} else if t == "" {
				// blank line between recipient list and status — keep scanning a few more lines
			} else if reEmail.MatchString(t) {
				// Bare address line in the Exchange failure block.
				for _, m := range reEmail.FindAllString(t, -1) {
					add(m)
				}
				continue
			}
		}
		if strings.Contains(l, "final-recipient") || strings.Contains(l, "original-recipient") ||
			strings.Contains(l, "failed recipient") || strings.Contains(l, "address(es) failed") ||
			strings.Contains(l, "recipients failed") || strings.Contains(l, "was not delivered to") ||
			strings.Contains(l, "could not be delivered to") || strings.Contains(l, "couldn't be delivered") ||
			strings.Contains(l, "delivery to the following") ||
			strings.HasPrefix(strings.TrimSpace(l), "recipient:") ||
			(strings.Contains(l, "host ") && strings.Contains(l, "said")) ||
			(reEmail.MatchString(line) && reSMTPCode.MatchString(line)) {
			for _, m := range reEmail.FindAllString(line, -1) {
				add(m)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, v := range hdr["x-failed-recipients"] {
		for _, p := range strings.Split(v, ",") {
			add(p)
		}
	}
	// Angle-bracket recipient lines common in Postfix/qmail dumps: <user@host>:
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "<") && strings.Contains(t, "@") {
			for _, m := range reEmail.FindAllString(line, -1) {
				add(m)
			}
		}
	}
	return out
}

func guessMTA(hdr ebmime.Header, subject, body string) string {
	text := strings.ToLower(subject + "\n" + body + "\n" + headerGet(hdr, "x-mailer") +
		headerGet(hdr, "x-failed-recipients"))
	from := strings.ToLower(headerGet(hdr, "from") + headerGet(hdr, "x-mailer") + joinFrom(nil))
	blob := text + "\n" + from
	checks := []struct {
		name string
		need []string
	}{
		{"postfix", []string{"postfix", "undelivered mail returned to sender", "this is the mail system at host"}},
		{"exim", []string{"exim", "this message was created automatically by mail delivery software"}},
		{"sendmail", []string{"sendmail", "the original message was received at"}},
		{"exchange", []string{"microsoft exchange", "microsoft outlook", "remote server returned", "delivery has failed to these recipients"}},
		{"gmail", []string{"delivery to the following recipient failed", "gmail", "google.com"}},
		{"yahoo", []string{"yahoo", "sorry, we were unable to deliver"}},
		{"qmail", []string{"qmail", "hi. this is the qmail"}},
		{"aruba", []string{"aruba.it", "smtps.aruba"}},
		{"register.it", []string{"register.it"}},
		{"libero", []string{"libero.it", "mail.libero"}},
	}
	for _, c := range checks {
		for _, n := range c.need {
			if strings.Contains(blob, n) {
				return c.name
			}
		}
	}
	return ""
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
