package bounce

import (
	"strings"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

var autoReplySubjects = []string{
	// EN
	"out of office", "out-of-office", "automatic reply", "auto reply", "autoreply",
	"auto-reply", "away from office", "on vacation", "on holiday", "vacation reply",
	"i am away", "i'm away", "currently away",
	// IT
	"fuori ufficio", "risposta automatica", "assenza", "sono assente", "in ferie",
	"non in ufficio", "risposta automatica del sistema",
	// DE
	"abwesenheitsnotiz", "automatische antwort", "out of office auto reply",
	"ich bin abwesend", "urlaub", "nicht im büro", "nicht im buero", "bin bis",
	// FR
	"absence du bureau", "réponse automatique",
	"reponse automatique", //nolint:misspell // FR unaccented subject form
	"absent du bureau", "je suis absent", "congés", "conges",
	// ES
	"fuera de la oficina", "respuesta automática", "respuesta automatica",
	"ausencia", "estoy ausente", "vacaciones",
}

// Body phrases that reinforce OOF even when the subject is terse.
var autoReplyBodyHints = []string{
	"abwesenheitsnotiz", "bin bis", "nicht im büro", "nicht im buero",
	"absent du bureau", "fuori ufficio", "fuera de la oficina",
	"out of office", "automatic reply", "risposta automatica",
}

func classifyAutoReply(hdr ebmime.Header, subject string, from []ebmime.Address, body string) *Result {
	// True list/newsletter traffic is never an auto-reply.
	if isListMessage(hdr) {
		return nil
	}
	ooo := subjectLooksAutoReply(subject) || bodyLooksAutoReply(body)
	if isAutoSubmitted(hdr) || hasAutoReplyHeader(hdr) || ooo {
		r := &Result{
			Kind:        KindAutoReply,
			IsAutoReply: true,
			Confidence:  0.95,
		}
		if ooo && !isAutoSubmitted(hdr) && !hasAutoReplyHeader(hdr) {
			r.Confidence = 0.85
		}
		_ = from
		return r
	}
	return nil
}

// isListMessage is true for mailing-list traffic. Precedence: bulk alone does NOT qualify —
// classic vacation programs set bulk together with Auto-Submitted / OOF subjects.
func isListMessage(hdr ebmime.Header) bool {
	if strings.TrimSpace(headerGet(hdr, "list-id")) != "" {
		return true
	}
	if strings.TrimSpace(headerGet(hdr, "list-unsubscribe")) != "" {
		return true
	}
	prec := strings.ToLower(strings.TrimSpace(headerGet(hdr, "precedence")))
	return prec == "list"
}

func isAutoSubmitted(hdr ebmime.Header) bool {
	v := strings.ToLower(strings.TrimSpace(headerGet(hdr, "auto-submitted")))
	if v == "" || v == "no" {
		return false
	}
	return strings.HasPrefix(v, "auto-")
}

func hasAutoReplyHeader(hdr ebmime.Header) bool {
	if v := headerGet(hdr, "x-autoreply"); v != "" && !strings.EqualFold(v, "no") {
		return true
	}
	if v := headerGet(hdr, "x-autorespond"); v != "" {
		return true
	}
	prec := strings.ToLower(headerGet(hdr, "precedence"))
	switch prec {
	case "auto_reply", "autoreply":
		return true
	}
	if v := headerGet(hdr, "x-auto-response-suppress"); v != "" {
		if strings.Contains(strings.ToLower(headerGet(hdr, "auto-submitted")), "auto") {
			return true
		}
	}
	return false
}

func subjectLooksAutoReply(subject string) bool {
	s := strings.ToLower(strings.TrimSpace(subject))
	if s == "" {
		return false
	}
	if looksLikeBounceSubject(s) {
		return false
	}
	for _, p := range autoReplySubjects {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func bodyLooksAutoReply(body string) bool {
	b := strings.ToLower(body)
	for _, p := range autoReplyBodyHints {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

func classifyChallenge(hdr ebmime.Header, subject string, from []ebmime.Address, body string) *Result {
	s := strings.ToLower(subject)
	b := strings.ToLower(body)
	fromL := ""
	if len(from) > 0 {
		fromL = strings.ToLower(from[0].Address + " " + from[0].Name)
	}
	blob := s + "\n" + b + "\n" + fromL

	named := []string{
		"boxbe", "approve this sender", "add to your guest list", "boxbe.com",
		"spamgourmet", "remaining stamps", "eaten message", "spamgourmet.com",
		"this message was discarded by spamgourmet",
		"tmda", "tagged message delivery agent", "mailfront challenge",
	}
	for _, p := range named {
		if strings.Contains(blob, p) {
			return &Result{Kind: KindChallengeResponse, Confidence: 0.92}
		}
	}

	signals := 0
	phrases := []string{
		"challenge-response", "challenge response", "confirm your message",
		"confirmation required", "please confirm", "release your message",
		"spam filter challenge", "send a blank reply", "verification required",
		"your message is being held", "moderated",
	}
	for _, p := range phrases {
		if strings.Contains(s, p) || strings.Contains(b, p) || strings.Contains(fromL, p) {
			signals++
		}
	}
	if strings.Contains(fromL, "challenge") || strings.Contains(s, "challenge") {
		signals++
	}
	if headerGet(hdr, "x-challenge") != "" || headerGet(hdr, "x-cr-challenge") != "" {
		signals += 2
	}
	if signals >= 1 && (strings.Contains(s, "confirm") || strings.Contains(s, "challenge") ||
		strings.Contains(s, "verification") || strings.Contains(b, "challenge-response") ||
		headerGet(hdr, "x-challenge") != "") {
		return &Result{Kind: KindChallengeResponse, Confidence: 0.9}
	}
	return nil
}
