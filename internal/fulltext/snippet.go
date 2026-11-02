package fulltext

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const ellipsis = '…'

// foldRune maps a rune to its case- and diacritic-insensitive form, one rune for one rune so positions in
// the folded text are positions in the original (é → e, Ü → u, ß stays ß).
func foldRune(r rune) rune {
	if r < utf8RuneSelf {
		return unicode.ToLower(r)
	}
	d := []rune(norm.NFD.String(string(r)))
	if len(d) > 0 && !unicode.Is(unicode.Mn, d[0]) {
		r = d[0]
	}
	return unicode.ToLower(r)
}

const utf8RuneSelf = 0x80

func foldRunes(in []rune) []rune {
	out := make([]rune, len(in))
	for i, r := range in {
		out[i] = foldRune(r)
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

// match is a [start, end) range in runes.
type match struct{ start, end int }

// findMatches returns the non-overlapping occurrences of the terms in folded, preferring whole-word hits:
// a term is searched as a whole word first and as any substring only when it never occurs as a word (MongoDB
// matched the message, so the term is somewhere — maybe glued to punctuation the tokenizer split on).
func findMatches(folded []rune, terms []string) []match {
	var all []match
	for _, t := range terms {
		ft := foldRunes([]rune(t))
		if len(ft) == 0 {
			continue
		}
		hits := occurrences(folded, ft, true)
		if len(hits) == 0 {
			hits = occurrences(folded, ft, false)
		}
		all = append(all, hits...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end > all[j].end
	})
	out := all[:0]
	last := -1
	for _, m := range all {
		if m.start < last {
			continue
		}
		out = append(out, m)
		last = m.end
	}
	return out
}

// occurrences finds needle in hay; wholeWord requires word boundaries on both sides.
func occurrences(hay, needle []rune, wholeWord bool) []match {
	var out []match
	n := len(needle)
	for i := 0; i+n <= len(hay); i++ {
		if hay[i] != needle[0] || !runesEqual(hay[i:i+n], needle) {
			continue
		}
		if wholeWord && ((i > 0 && isWordRune(hay[i-1])) || (i+n < len(hay) && isWordRune(hay[i+n]))) {
			continue
		}
		out = append(out, match{i, i + n})
		i += n - 1
	}
	return out
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildSnippet returns a plain-text excerpt of text of about width code points around the first match of
// the terms, with … where it was cut, and the matches inside it. Without a match in the text (the message
// matched on its subject or sender) it is the start of the text without highlights.
func buildSnippet(text string, terms []string, width int) (string, []FullTextHighlight) {
	src := []rune(strings.Join(strings.Fields(text), " "))
	if len(src) == 0 {
		return "", []FullTextHighlight{}
	}
	matches := findMatches(foldRunes(src), terms)
	start := 0
	if len(matches) > 0 {
		// Put the first match about a third into the window, starting at a word.
		start = max(0, matches[0].start-width/3)
		if start > 0 {
			if sp := firstSpace(src, start, matches[0].start); sp >= 0 {
				start = sp + 1
			}
		}
	}
	end := min(len(src), start+width)
	if len(matches) > 0 && end < matches[0].end {
		end = matches[0].end // never cut inside the first match
	}
	if end < len(src) {
		if sp := lastSpace(src, start+width/2, end); sp > start && sp >= firstEnd(matches) {
			end = sp
		}
	}
	// src comes from strings.Fields, and start is 0 or just after a space: the window never starts with a
	// space, so match offsets only shift by the leading ellipsis.
	var b strings.Builder
	offset := 0
	if start > 0 {
		b.WriteRune(ellipsis)
		offset = 1
	}
	b.WriteString(strings.TrimRightFunc(string(src[start:end]), unicode.IsSpace))
	if end < len(src) {
		b.WriteRune(ellipsis)
	}
	hl := []FullTextHighlight{}
	for _, m := range matches {
		if m.start < start || m.end > end {
			continue
		}
		hl = append(hl, FullTextHighlight{Start: m.start - start + offset, End: m.end - start + offset})
	}
	return b.String(), hl
}

// firstEnd is the end of the first match (0 without matches).
func firstEnd(ms []match) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[0].end
}

// firstSpace returns the index of the first space in src[from:to), or -1.
func firstSpace(src []rune, from, to int) int {
	for i := max(from, 0); i < min(to, len(src)); i++ {
		if src[i] == ' ' {
			return i
		}
	}
	return -1
}

// lastSpace returns the index of the last space in src[from:to), or -1.
func lastSpace(src []rune, from, to int) int {
	for i := min(to, len(src)) - 1; i >= max(from, 0); i-- {
		if src[i] == ' ' {
			return i
		}
	}
	return -1
}
