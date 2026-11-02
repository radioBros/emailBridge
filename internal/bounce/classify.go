package bounce

import (
	"bytes"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

// Classify classifies raw RFC 822 bytes. Malformed input yields KindNone (never panics).
func Classify(raw []byte) (*Result, error) {
	out := &Result{Kind: KindNone, Confidence: 0}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}

	hdr, err := ebmime.ParseHeaders(raw)
	if err != nil {
		hdr = ebmime.Header{}
	}
	subject, _ := ebmime.DecodeSubject(hdr)
	fromAddrs, _ := ebmime.DecodeAddresses(hdr, "from")

	entity, entErr := ebmime.ParseEntity(raw)
	ctype, params := contentType(hdr, entity)

	// 1. multipart/report
	if strings.HasPrefix(ctype, "multipart/report") && entity != nil && entErr == nil {
		rt := strings.ToLower(params["report-type"])
		switch rt {
		case "delivery-status":
			if r := parseDSN(entity, hdr, subject); r != nil {
				return r, nil
			}
		case "feedback-report":
			if r := parseARF(entity, hdr); r != nil {
				return r, nil
			}
		}
	}

	plain, html := bodyText(entity, hdr, raw)
	body := plain
	if body == "" {
		body = html
	}

	// 2. Heuristic bounce (before auto-reply — many bounces also set Auto-Submitted)
	if r := classifyHeuristicBounce(hdr, subject, fromAddrs, body); r != nil {
		enrichCorrelation(r, hdr, entity, body)
		return r, nil
	}

	// 3. Challenge-response
	if r := classifyChallenge(hdr, subject, fromAddrs, body); r != nil {
		enrichCorrelation(r, hdr, entity, body)
		return r, nil
	}

	// 4. Auto-reply
	if r := classifyAutoReply(hdr, subject, fromAddrs, body); r != nil {
		enrichCorrelation(r, hdr, entity, body)
		return r, nil
	}

	return out, nil
}

func contentType(hdr ebmime.Header, entity *message.Entity) (string, map[string]string) {
	if entity != nil {
		ctype, params, err := entity.Header.ContentType()
		if err == nil && ctype != "" {
			return strings.ToLower(ctype), params
		}
	}
	vals := hdr["content-type"]
	if len(vals) == 0 {
		return "text/plain", nil
	}
	ctype, params, err := mime.ParseMediaType(vals[0])
	if err != nil {
		return strings.ToLower(vals[0]), nil
	}
	return strings.ToLower(ctype), params
}

func bodyText(entity *message.Entity, hdr ebmime.Header, raw []byte) (plain, html string) {
	if entity != nil {
		p, h, _, err := ebmime.TextParts(entity, 64<<10)
		if err == nil {
			return p, h
		}
	}
	// Fallback: strip headers and take raw body (best-effort for fuzz / broken MIME).
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return string(raw[i+4:]), ""
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return string(raw[i+2:]), ""
	}
	_ = hdr
	return "", ""
}

func enrichCorrelation(r *Result, hdr ebmime.Header, entity *message.Entity, body string) {
	if r.OriginalMessageID == "" {
		r.OriginalMessageID = firstMessageID(hdr["in-reply-to"])
		if r.OriginalMessageID == "" {
			r.OriginalMessageID = firstMessageID(hdr["references"])
		}
	}
	if r.OutboundID == "" {
		r.OutboundID = outboundFromHeaders(hdr)
	}
	if r.OutboundID == "" && entity != nil {
		r.OutboundID = outboundFromEntity(entity)
	}
	if r.OutboundID == "" {
		r.OutboundID = outboundFromVERP(hdr, r.FinalRecipients, body)
	}
	if len(r.FinalRecipients) == 0 {
		if addrs := extractAddrsFromVERP(hdr); len(addrs) > 0 {
			r.FinalRecipients = addrs
		}
	}
}

func readPart(e *message.Entity, max int) string {
	if e == nil || e.Body == nil {
		return ""
	}
	if max <= 0 {
		max = 64 << 10
	}
	b, err := io.ReadAll(io.LimitReader(e.Body, int64(max)+1))
	if err != nil {
		return ""
	}
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

func firstMessageID(vals []string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// References can list several ids; take the first token with @.
		for _, tok := range strings.Fields(v) {
			tok = strings.Trim(tok, "<> \t")
			if strings.Contains(tok, "@") {
				if !strings.HasPrefix(tok, "<") {
					return "<" + tok + ">"
				}
				return tok
			}
		}
		if strings.Contains(v, "@") {
			return v
		}
	}
	return ""
}

func headerGet(hdr ebmime.Header, name string) string {
	vals := hdr[strings.ToLower(name)]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
