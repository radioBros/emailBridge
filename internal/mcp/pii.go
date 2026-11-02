package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

var emailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)

// RedactJSON hashes emails and truncates string values over maxChars in a JSON blob.
func RedactJSON(raw string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 4000
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return redactPlain(raw, maxChars)
	}
	v = walkRedact(v, maxChars)
	b, err := json.Marshal(v)
	if err != nil {
		return redactPlain(raw, maxChars)
	}
	return string(b)
}

func walkRedact(v any, maxChars int) any {
	switch t := v.(type) {
	case string:
		return redactPlain(t, maxChars)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = walkRedact(x, maxChars)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = walkRedact(x, maxChars)
		}
		return out
	default:
		return v
	}
}

// HashEmails replaces e-mail addresses by hashes and keeps everything else (no truncation).
func HashEmails(s string) string {
	return emailRE.ReplaceAllStringFunc(s, hashAddr)
}

func redactPlain(s string, maxChars int) string {
	out := emailRE.ReplaceAllStringFunc(s, hashAddr)
	if utf8.RuneCountInString(out) <= maxChars {
		return out
	}
	return string([]rune(out)[:maxChars]) + "…"
}

func hashAddr(addr string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return "addr_" + hex.EncodeToString(sum[:8])
}
