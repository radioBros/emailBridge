package embeddings

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// sourceText is what is embedded for a message: the subject, a blank line, the body text (G3 already
// collapsed its whitespace).
func sourceText(subject, body string) string {
	subject, body = strings.TrimSpace(subject), strings.TrimSpace(body)
	switch {
	case subject == "":
		return body
	case body == "":
		return subject
	}
	return subject + "\n\n" + body
}

// textHash identifies what a message's vectors were computed from (model + text), so an unchanged message is
// not sent to the provider again.
func textHash(model, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + text))
	return hex.EncodeToString(sum[:16])
}

// chunkText cuts text into chunks of about size code points, at most maxChunks. A chunk ends at a line break,
// else after a sentence, else at a space within the last 30 % of the window (a run without spaces is cut
// hard); the next chunk repeats the last ~overlap code points, starting at a word. Chunks are trimmed and
// never empty.
func chunkText(text string, size, overlap, maxChunks int) []string {
	r := []rune(text)
	n := len(r)
	if size <= 0 || maxChunks <= 0 {
		return nil
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	var out []string
	start := 0
	for start < n && len(out) < maxChunks {
		end := min(start+size, n)
		if end < n {
			end = breakPoint(r, start, end, size)
		}
		if c := strings.TrimSpace(string(r[start:end])); c != "" {
			out = append(out, c)
		}
		if end >= n {
			break
		}
		next := max(end-overlap, start+1)
		// Start the overlap at a word: skip forward to the next space (bounded by end).
		if next > 0 && !unicode.IsSpace(r[next-1]) {
			for k := next; k < end; k++ {
				if unicode.IsSpace(r[k]) {
					next = k + 1
					break
				}
			}
		}
		for next < end && unicode.IsSpace(r[next]) {
			next++
		}
		if next >= end { // no word start inside the overlap: continue right after the chunk
			next = end
		}
		start = next
	}
	return out
}

// breakPoint picks where a chunk [start, end) of a longer text ends (exclusive), preferring a line break,
// then a sentence end, then a space, within the last 30 % of the window.
func breakPoint(r []rune, start, end, size int) int {
	floor := max(start+1, end-size*3/10)
	for _, pick := range []func(i int) bool{
		func(i int) bool { return r[i] == '\n' },
		func(i int) bool {
			return (r[i] == '.' || r[i] == '!' || r[i] == '?' || r[i] == '。' || r[i] == ';') && i+1 < len(r) && unicode.IsSpace(r[i+1])
		},
		func(i int) bool { return unicode.IsSpace(r[i]) },
	} {
		for i := end - 1; i >= floor; i-- {
			if pick(i) {
				return i + 1
			}
		}
	}
	return end
}

// snippet shortens a chunk to about n code points, cutting at a space and marking the cut with an ellipsis.
func snippet(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	cut := n
	for i := n; i > n*2/3; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}
