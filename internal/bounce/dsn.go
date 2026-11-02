package bounce

import (
	"bufio"
	"strings"

	"github.com/emersion/go-message"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

func parseDSN(entity *message.Entity, top ebmime.Header, subject string) *Result {
	parts := readMultipartParts(entity)
	if len(parts) < 2 {
		return nil
	}
	var dsBody, humanBody, returnedBody string
	var returnedCT string
	for _, p := range parts {
		ctype := strings.ToLower(p.ContentType)
		switch {
		case ctype == "message/delivery-status":
			dsBody = p.Body
		case ctype == "message/rfc822" || ctype == "text/rfc822-headers" ||
			strings.HasPrefix(ctype, "message/"):
			if returnedBody == "" {
				returnedBody = p.Body
				returnedCT = ctype
			}
		case strings.HasPrefix(ctype, "text/"):
			if humanBody == "" {
				humanBody = p.Body
			}
		}
	}
	if dsBody == "" {
		return nil
	}

	fields := parseDSNFields(dsBody)
	var recipients []Recipient
	var finals []string
	addRec := func(block map[string]string) {
		rec := recipientFromDSN(block)
		if rec.Address == "" && rec.Status == "" && rec.Action == "" {
			return
		}
		recipients = append(recipients, rec)
		if rec.Address != "" {
			finals = append(finals, rec.Address)
		}
	}
	// RFC 3464: first block is per-message; later blocks are per-recipient.
	// Real MTAs often put Final-Recipient in the first block (no separate per-msg fields).
	start := 1
	if len(fields) > 0 {
		if _, ok := fields[0]["final-recipient"]; ok {
			start = 0
		} else if _, ok := fields[0]["original-recipient"]; ok {
			start = 0
		}
	}
	for _, block := range fields[start:] {
		addRec(block)
	}
	if len(recipients) == 0 && len(fields) > 0 {
		addRec(fields[0])
	}
	if len(recipients) == 0 {
		return nil
	}

	bt := dominantBounceType(recipients)
	if bt == "" {
		bt = bounceTypeFromSubjectBody(subject, humanBody)
	}
	r := &Result{
		Kind:            KindDSNBounce,
		BounceType:      bt,
		Recipients:      recipients,
		FinalRecipients: finals,
		Confidence:      0.99,
		MTA:             guessMTA(top, subject, humanBody),
	}
	if returnedBody != "" {
		fillFromReturnedBytes(r, returnedBody, returnedCT)
	}
	enrichCorrelation(r, top, entity, humanBody)
	if r.OriginalMessageID == "" {
		r.OriginalMessageID = firstMessageID(top["in-reply-to"])
	}
	return r
}

func recipientFromDSN(block map[string]string) Recipient {
	addr := dsnAddress(block["final-recipient"])
	if addr == "" {
		addr = dsnAddress(block["original-recipient"])
	}
	action := strings.ToLower(strings.TrimSpace(block["action"]))
	status := strings.TrimSpace(block["status"])
	diag := strings.TrimSpace(block["diagnostic-code"])
	if len(diag) > 512 {
		diag = diag[:512]
	}
	bt := bounceTypeFromStatus(status, diag, action)
	return Recipient{
		Address:    strings.ToLower(addr),
		Action:     action,
		Status:     status,
		Diagnostic: diag,
		BounceType: bt,
	}
}

// parseDSNFields splits a delivery-status body into per-message (index 0) and
// per-recipient blocks (RFC 3464).
func parseDSNFields(body string) []map[string]string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	chunks := strings.Split(body, "\n\n")
	out := make([]map[string]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		m := parseHeaderBlock(chunk)
		if len(m) > 0 {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return []map[string]string{{}}
	}
	return out
}

func parseHeaderBlock(block string) map[string]string {
	m := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(block))
	var key, val string
	flush := func() {
		if key == "" {
			return
		}
		m[key] = strings.TrimSpace(val)
		key, val = "", ""
	}
	for sc.Scan() {
		line := sc.Text()
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			val += " " + strings.TrimSpace(line)
			continue
		}
		flush()
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(line[:i]))
		val = strings.TrimSpace(line[i+1:])
	}
	flush()
	return m
}

func dsnAddress(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// "rfc822; user@example.com" or "rfc822;user@example.com"
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = strings.TrimSpace(v[i+1:])
	}
	v = strings.Trim(v, "<> \t")
	return v
}

func bounceTypeFromStatus(status, diagnostic, action string) BounceType {
	status = strings.TrimSpace(status)
	class := ""
	subject := ""
	if status != "" {
		parts := strings.SplitN(status, ".", 3)
		if len(parts) >= 1 {
			class = parts[0]
		}
		if len(parts) >= 3 {
			subject = parts[1] + "." + parts[2]
		} else if len(parts) == 2 {
			subject = parts[1]
		}
		// Sometimes "5.1.1 (permanent failure)" — take token before space.
		if i := strings.IndexAny(class, " \t("); i > 0 {
			class = class[:i]
		}
		if i := strings.IndexAny(status, " \t"); i > 0 {
			status = status[:i]
			parts = strings.SplitN(status, ".", 3)
			if len(parts) >= 1 {
				class = parts[0]
			}
			if len(parts) >= 3 {
				subject = parts[1] + "." + parts[2]
			}
		}
	}
	diagL := strings.ToLower(diagnostic + " " + action)
	switch class {
	case "2":
		return ""
	case "4":
		return BounceSoft
	case "5":
		if subject == "2.2" || strings.Contains(diagL, "mailbox full") ||
			strings.Contains(diagL, "quota") || strings.Contains(diagL, "over quota") {
			return BounceSoft
		}
		return BounceHard
	}
	return bounceTypeFromSubjectBody("", diagnostic)
}

func dominantBounceType(recs []Recipient) BounceType {
	var soft, hard bool
	for _, r := range recs {
		switch r.BounceType {
		case BounceHard:
			hard = true
		case BounceSoft:
			soft = true
		}
	}
	if hard {
		return BounceHard
	}
	if soft {
		return BounceSoft
	}
	return ""
}

func fillFromReturnedBytes(r *Result, raw, ctype string) {
	raw = ensureBlankLine(raw)
	inner, err := ebmime.ParseHeaders([]byte(raw))
	if err != nil {
		return
	}
	if r.OriginalMessageID == "" {
		r.OriginalMessageID = ebmime.MessageIDHeader(inner)
	}
	if r.OutboundID == "" {
		r.OutboundID = outboundFromHeaders(inner)
	}
	_ = ctype
}

// partData holds one multipart part with its body already consumed
// (go-message requires reading each part before NextPart).
type partData struct {
	ContentType string
	Body        string
	Header      map[string]string
}

func readMultipartParts(e *message.Entity) []partData {
	if e == nil {
		return nil
	}
	mr := e.MultipartReader()
	if mr == nil {
		return nil
	}
	var out []partData
	for {
		p, err := mr.NextPart()
		if err != nil || p == nil {
			break
		}
		ctype, _, _ := p.Header.ContentType()
		body := readPart(p, 64<<10)
		h := map[string]string{}
		// Capture a few useful headers from the part itself (rare).
		for _, k := range []string{"Message-Id", "X-EmailBridge-Outbound-Id"} {
			if v := p.Header.Get(k); v != "" {
				h[strings.ToLower(k)] = v
			}
		}
		out = append(out, partData{ContentType: ctype, Body: body, Header: h})
	}
	return out
}
