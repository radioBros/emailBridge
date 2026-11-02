package submit

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

const (
	actionReply    = "reply"
	actionReplyAll = "replyAll"
	actionForward  = "forward"
)

func (s *Service) applyReference(ctx context.Context, accountID string, acc *SMTPAccount, req SubmitRequest) (SubmitRequest, error) {
	ref := req.Reference
	if ref == nil {
		return req, nil
	}
	switch ref.Action {
	case actionReply, actionReplyAll, actionForward:
	default:
		return req, errs.New(errs.CodeReferenceInvalid, "reference.action must be reply, replyAll or forward.").
			WithSafe("action", ref.Action)
	}
	if ref.MessageID == "" {
		return req, errs.New(errs.CodeReferenceInvalid, "reference.messageId is required.")
	}
	if s.Messages == nil {
		return req, errs.New(errs.CodeMessageNotFound, "The referenced message cannot be loaded.")
	}
	raw, err := s.Messages.Raw(ctx, accountID, ref.MessageID)
	if err != nil {
		return req, err
	}
	orig, err := parseOriginal(raw)
	if err != nil {
		return req, errs.New(errs.CodeReferenceInvalid, "The referenced message could not be parsed.")
	}
	inline := true
	if ref.Inline != nil {
		inline = *ref.Inline
	}
	if req.Headers == nil {
		req.Headers = map[string]string{}
	}
	switch ref.Action {
	case actionReply, actionReplyAll:
		req = applyReply(req, acc, orig, ref.Action == actionReplyAll)
		if inline {
			req.Text, req.HTML = quoteBodies(req.Text, req.HTML, orig)
		}
	case actionForward:
		req.Subject = prefixSubject("Fwd:", firstNonEmpty(req.Subject, orig.Subject))
		if !inline {
			req.Attachments = append(req.Attachments, AttachmentInput{
				Filename: "forwarded.eml", ContentType: "message/rfc822",
				Content: base64.StdEncoding.EncodeToString(raw), Disposition: "attachment",
			})
		} else {
			req.Text, req.HTML = quoteBodies(req.Text, req.HTML, orig)
		}
		if len(req.To) == 0 && len(req.Cc) == 0 && len(req.Bcc) == 0 {
			return req, errs.New(errs.CodeValidationFailed, "A forward needs at least one recipient.")
		}
	}
	req.Reference = nil
	return req, nil
}

func applyReply(req SubmitRequest, acc *SMTPAccount, orig *originalMessage, replyAll bool) SubmitRequest {
	req.Subject = prefixSubject("Re:", firstNonEmpty(req.Subject, orig.Subject))
	if orig.MessageID != "" {
		req.Headers["In-Reply-To"] = orig.MessageID
		refs := orig.References
		if refs == "" {
			refs = orig.MessageID
		} else if !strings.Contains(refs, orig.MessageID) {
			refs = strings.TrimSpace(refs + " " + orig.MessageID)
		}
		req.Headers["References"] = refs
	}
	if len(req.To) == 0 && len(req.Cc) == 0 && len(req.Bcc) == 0 {
		to := orig.ReplyTo
		if len(to) == 0 {
			to = orig.From
		}
		req.To = toAddresses(to)
		if replyAll {
			seen := map[string]bool{}
			self := strings.ToLower(acc.Email)
			add := func(list []AddressInput) {
				for _, a := range list {
					k := strings.ToLower(a.Address)
					if k == "" || k == self || seen[k] {
						continue
					}
					seen[k] = true
					req.Cc = append(req.Cc, a)
				}
			}
			for _, a := range req.To {
				seen[strings.ToLower(a.Address)] = true
			}
			add(toAddresses(orig.To))
			add(toAddresses(orig.Cc))
		}
	}
	return req
}

func quoteBodies(text, html string, orig *originalMessage) (string, string) {
	attr := fmt.Sprintf("On %s, %s wrote:", orig.Date, formatFrom(orig.From))
	if text != "" || orig.Text != "" {
		var b strings.Builder
		if text != "" {
			b.WriteString(text)
			b.WriteString("\r\n\r\n")
		}
		b.WriteString(attr)
		b.WriteString("\r\n")
		for line := range strings.Lines(orig.Text) {
			b.WriteString("> ")
			b.WriteString(strings.TrimRight(line, "\r\n"))
			b.WriteString("\r\n")
		}
		text = b.String()
	}
	if html != "" || orig.HTML != "" {
		var b strings.Builder
		if html != "" {
			b.WriteString(html)
		}
		b.WriteString(`<br><br><div class="eb-quote-attr">`)
		b.WriteString(htmlEscape(attr))
		b.WriteString(`</div><blockquote type="cite">`)
		if orig.HTML != "" {
			b.WriteString(orig.HTML)
		} else {
			b.WriteString("<pre>")
			b.WriteString(htmlEscape(orig.Text))
			b.WriteString("</pre>")
		}
		b.WriteString(`</blockquote>`)
		html = b.String()
	}
	return text, html
}

type originalMessage struct {
	MessageID  string
	References string
	Subject    string
	Date       string
	From       []mime.Address
	ReplyTo    []mime.Address
	To         []mime.Address
	Cc         []mime.Address
	Text       string
	HTML       string
}

func parseOriginal(raw []byte) (*originalMessage, error) {
	h, err := mime.ParseHeaders(raw)
	if err != nil {
		return nil, err
	}
	out := &originalMessage{
		MessageID:  mime.MessageIDHeader(h),
		References: firstHeader(h, "references"),
		Date:       firstHeader(h, "date"),
	}
	if subj, err := mime.DecodeSubject(h); err == nil {
		out.Subject = subj
	}
	out.From, _ = mime.DecodeAddresses(h, "from")
	out.ReplyTo, _ = mime.DecodeAddresses(h, "reply-to")
	out.To, _ = mime.DecodeAddresses(h, "to")
	out.Cc, _ = mime.DecodeAddresses(h, "cc")
	ent, err := mime.ParseEntity(raw)
	if err == nil && ent != nil {
		out.Text, out.HTML, _, _ = mime.TextParts(ent, mime.DefaultTextMax)
	}
	return out, nil
}

func firstHeader(h mime.Header, name string) string {
	vals := h[name]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func toAddresses(in []mime.Address) []AddressInput {
	out := make([]AddressInput, 0, len(in))
	for _, a := range in {
		if a.Address == "" {
			continue
		}
		out = append(out, AddressInput{Name: a.Name, Address: a.Address})
	}
	return out
}

func formatFrom(from []mime.Address) string {
	if len(from) == 0 {
		return "someone"
	}
	a := from[0]
	if a.Name != "" {
		return fmt.Sprintf("%s <%s>", a.Name, a.Address)
	}
	return a.Address
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		`&`, "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&quot;", `'`, "&#39;",
	)
	return replacer.Replace(s)
}
