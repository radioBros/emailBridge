package bounce

import (
	"strings"

	"github.com/emersion/go-message"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

func parseARF(entity *message.Entity, top ebmime.Header) *Result {
	parts := readMultipartParts(entity)
	if len(parts) < 2 {
		return nil
	}
	var feedbackBody, returnedBody, returnedCT string
	for _, p := range parts {
		ctype := strings.ToLower(p.ContentType)
		switch {
		case ctype == "message/feedback-report":
			feedbackBody = p.Body
		case ctype == "message/rfc822" || ctype == "text/rfc822-headers" ||
			strings.HasPrefix(ctype, "message/"):
			if returnedBody == "" {
				returnedBody = p.Body
				returnedCT = ctype
			}
		}
	}
	if feedbackBody == "" {
		return nil
	}
	fields := parseHeaderBlock(feedbackBody)
	ft := strings.ToLower(strings.TrimSpace(fields["feedback-type"]))
	if ft == "" {
		ft = "abuse"
	}
	addr := dsnAddress(fields["final-recipient"])
	if addr == "" {
		addr = strings.ToLower(strings.Trim(fields["original-mail-from"], "<> "))
	}
	r := &Result{
		Kind:         KindComplaint,
		FeedbackType: ft,
		Confidence:   0.99,
	}
	if addr != "" {
		r.FinalRecipients = []string{strings.ToLower(addr)}
		r.Recipients = []Recipient{{Address: strings.ToLower(addr)}}
	}
	if mid := strings.TrimSpace(fields["original-message-id"]); mid != "" {
		if !strings.HasPrefix(mid, "<") && strings.Contains(mid, "@") {
			mid = "<" + mid + ">"
		}
		r.OriginalMessageID = mid
	}
	if returnedBody != "" {
		fillFromReturnedBytes(r, returnedBody, returnedCT)
	}
	enrichCorrelation(r, top, entity, "")
	return r
}
