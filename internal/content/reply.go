package content

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var (
	// attributionLine matches "On … wrote:" style headers in common languages.
	attributionLine = regexp.MustCompile(`(?i)^\s*(` +
		`on\s+.+\s+wrote\s*:|` +
		`il\s+.+\s+ha\s+scritto\s*:|` +
		`am\s+.+\s+schrieb\s+.+:|` +
		`le\s+.+\s+a\s+[eé]crit\s*:|` +
		`el\s+.+\s+escribi[oó]\s*:|` +
		`den\s+.+\s+skrev\s+.+:` +
		`)\s*$`)

	outlookSep = regexp.MustCompile(`(?i)^\s*(` +
		`-+\s*original\s+message\s*-+|` +
		`-+\s*messaggio\s+originale\s*-+|` +
		`-+\s*message\s+d['']origine\s*-+|` +
		`-+\s*ursprüngliche\s+nachricht\s*-+|` +
		`_{5,}` +
		`)\s*$`)

	signatureDelim = regexp.MustCompile(`^-- $`)
)

// ReplyText extracts the new text of a reply from a plain-text body (P4.2 §4).
func ReplyText(plain string) string {
	if plain == "" {
		return ""
	}
	lines := splitLines(plain)
	cut := len(lines)
	for i, line := range lines {
		if outlookSep.MatchString(line) || attributionLine.MatchString(line) || signatureDelim.MatchString(line) {
			cut = i
			break
		}
	}
	lines = lines[:cut]
	var kept []string
	for _, line := range lines {
		trimLeft := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimLeft, ">") {
			continue
		}
		kept = append(kept, line)
	}
	return collapseBlank(strings.Join(kept, "\n"))
}

// ReplyTextFromHTML converts HTML to text then applies ReplyText.
func ReplyTextFromHTML(raw string) string {
	return ReplyText(htmlToText(raw))
}

func htmlToText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return stripTagsFallback(raw)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "head":
				return
			case "br", "hr":
				b.WriteByte('\n')
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
				b.WriteByte('\n')
			}
		}
	}
	walk(doc)
	return b.String()
}

func stripTagsFallback(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

func collapseBlank(s string) string {
	lines := splitLines(s)
	var out []string
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			// keep at most one blank line between blocks (≥3 → 2 total lines ⇒ 1 gap)
			if blankRun <= 1 {
				out = append(out, "")
			}
			continue
		}
		blankRun = 0
		out = append(out, line)
	}
	// trim leading/trailing blanks
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
