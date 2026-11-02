package content

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// subjectPrefix matches a leading reply/forward token and optional [n] before the colon.
// Longer tokens first so "Fwd:" is not eaten as "Fw"+"d:".
var subjectPrefix = regexp.MustCompile(`(?i)^(inoltro|doorst|antw|fwd|rif|res|enc|aw|wg|tr|rv|sv|vs|vb|fw|re|r|i)\s*(?:\[\d+\])?\s*:\s*`)

// ThreadID returns a deterministic 24-hex conversation id for non-Gmail servers.
//
// Algorithm (docs/spec/P4.2-content.md §1):
//  1. Root = first References token, else In-Reply-To, else Message-ID.
//  2. Normalise with angle brackets (same rule as imapc.MsgID).
//  3. If non-empty → sha256, first 12 bytes as hex.
//  4. Else hash "subject:" + NormalizeSubject(subject).
func ThreadID(messageID, inReplyTo string, references []string, subject string) string {
	root := ""
	for _, r := range references {
		if t := strings.TrimSpace(r); t != "" {
			root = t
			break
		}
	}
	if root == "" {
		root = strings.TrimSpace(inReplyTo)
	}
	if root == "" {
		root = strings.TrimSpace(messageID)
	}
	if id := normMsgID(root); id != "" {
		return hashKey(id)
	}
	return hashKey("subject:" + NormalizeSubject(subject))
}

// NormalizeSubject strips common reply/forward prefixes and collapses whitespace.
func NormalizeSubject(subject string) string {
	s := strings.TrimSpace(subject)
	for {
		loc := subjectPrefix.FindStringIndex(s)
		if loc == nil || loc[0] != 0 {
			break
		}
		s = strings.TrimSpace(s[loc[1]:])
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func normMsgID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}

func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:12])
}
