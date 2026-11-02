package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

var emailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)

// RedactText hashes email addresses and truncates to maxChars (UTF-8 safe).
func RedactText(s string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultBodyMax
	}
	out := emailRE.ReplaceAllStringFunc(s, hashAddr)
	if utf8.RuneCountInString(out) <= maxChars {
		return out
	}
	runes := []rune(out)
	return string(runes[:maxChars]) + "…"
}

func hashAddr(addr string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return "addr_" + hex.EncodeToString(sum[:8])
}

// Truncate only (no hashing) — used when piiRedact is off but body must still be capped.
func Truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultBodyMax
	}
	if utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	return string([]rune(s)[:maxChars]) + "…"
}
