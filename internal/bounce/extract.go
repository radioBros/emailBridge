package bounce

import (
	"regexp"
	"strings"

	"github.com/emersion/go-message"

	ebmime "github.com/radiobros/emailbridge/internal/mailproto/mime"
)

var (
	reOutboundID = regexp.MustCompile(`(?i)\b(out_[0-9a-hjkmnp-z]{26})\b`)
	// bounce+out_<ulid>=user=domain@host  or  prvs=… / srs0=… ignored
	reVERP       = regexp.MustCompile(`(?i)(?:bounce\+)?(out_[0-9a-hjkmnp-z]{26})=([^=@]+)=([^=@]+)@`)
	reVERPSimple = regexp.MustCompile(`(?i)(out_[0-9a-hjkmnp-z]{26})=([^=\s]+)=([^=\s]+)`)
)

func outboundFromHeaders(hdr ebmime.Header) string {
	for _, key := range []string{"x-emailbridge-outbound-id", "x-eb-outbound-id"} {
		if v := strings.TrimSpace(headerGet(hdr, key)); v != "" {
			if m := reOutboundID.FindStringSubmatch(v); len(m) == 2 {
				return strings.ToLower(m[1])
			}
			if strings.HasPrefix(strings.ToLower(v), "out_") {
				return strings.ToLower(v)
			}
		}
	}
	return ""
}

func outboundFromEntity(entity *message.Entity) string {
	if entity == nil {
		return ""
	}
	// Note: only safe when the multipart body has not been consumed yet.
	for _, p := range readMultipartParts(entity) {
		if oid := strings.TrimSpace(p.Header["x-emailbridge-outbound-id"]); oid != "" {
			if m := reOutboundID.FindStringSubmatch(oid); len(m) == 2 {
				return strings.ToLower(m[1])
			}
		}
		if strings.HasPrefix(strings.ToLower(p.ContentType), "message/") ||
			strings.EqualFold(p.ContentType, "text/rfc822-headers") {
			inner, err := ebmime.ParseHeaders([]byte(ensureBlankLine(p.Body)))
			if err == nil {
				if id := outboundFromHeaders(inner); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

func ensureBlankLine(raw string) string {
	if strings.Contains(raw, "\r\n\r\n") || strings.Contains(raw, "\n\n") {
		return raw
	}
	return raw + "\r\n\r\n"
}

func outboundFromVERP(hdr ebmime.Header, finals []string, body string) string {
	candidates := []string{
		headerGet(hdr, "return-path"),
		headerGet(hdr, "x-original-to"),
		headerGet(hdr, "delivered-to"),
		body,
	}
	candidates = append(candidates, finals...)
	for _, c := range candidates {
		if id, _ := parseVERP(c); id != "" {
			return id
		}
	}
	return ""
}

func extractAddrsFromVERP(hdr ebmime.Header) []string {
	for _, key := range []string{"return-path", "x-original-to", "delivered-to"} {
		if _, addr := parseVERP(headerGet(hdr, key)); addr != "" {
			return []string{addr}
		}
	}
	return nil
}

func parseVERP(s string) (outboundID, addr string) {
	s = strings.Trim(s, "<> \t")
	if s == "" {
		return "", ""
	}
	if m := reVERP.FindStringSubmatch(s); len(m) == 4 {
		return strings.ToLower(m[1]), strings.ToLower(m[2] + "@" + m[3])
	}
	if m := reVERPSimple.FindStringSubmatch(s); len(m) == 4 {
		return strings.ToLower(m[1]), strings.ToLower(m[2] + "@" + m[3])
	}
	if m := reOutboundID.FindStringSubmatch(s); len(m) == 2 {
		return strings.ToLower(m[1]), ""
	}
	return "", ""
}
