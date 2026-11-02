package fulltext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/radiobros/emailbridge/internal/index"
)

// bodyText turns the fetched body parts into the stored text: the plain part when it has words, else the
// HTML part converted to text; whitespace collapsed, invalid UTF-8 dropped, cut at MaxTextBytes.
func bodyText(plain, htmlBody string) (text string, truncated bool) {
	src := plain
	if strings.TrimSpace(src) == "" && strings.TrimSpace(htmlBody) != "" {
		src = htmlToText(htmlBody)
	}
	return truncateUTF8(normalizeSpace(strings.ToValidUTF8(src, "")), MaxTextBytes)
}

// normalizeSpace collapses runs of horizontal whitespace to one space and runs of line breaks (with the blank
// lines between them) to one newline, and trims the ends. Control characters other than tab and newline go.
func normalizeSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace, pendingNL := false, false
	for _, r := range s {
		switch {
		case r == '\n':
			pendingNL = true
		case r == '\r':
			// CRLF: the \n follows; a lone \r is a line break too.
			pendingNL = true
		case unicode.IsSpace(r):
			pendingSpace = true
		case unicode.IsControl(r):
			continue
		default:
			if b.Len() > 0 {
				switch {
				case pendingNL:
					b.WriteByte('\n')
				case pendingSpace:
					b.WriteByte(' ')
				}
			}
			pendingSpace, pendingNL = false, false
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// htmlToText extracts the visible text of an HTML body: script, style and head are dropped, block elements
// and <br> become line breaks. Unparsable input falls back to the raw string with tags left as text, which is
// still searchable.
func htmlToText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		block := false
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "head", "title", "noscript", "template":
				return
			case "br", "hr":
				b.WriteByte('\n')
			case "p", "div", "tr", "li", "ul", "ol", "table", "section", "article", "header", "footer",
				"h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
				block = true
				b.WriteByte('\n')
			case "td", "th":
				b.WriteByte(' ')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return b.String()
}

// addrString flattens an indexed address for the text index: "Name <address>", or the bare address.
func addrString(a *index.Address) string {
	if a == nil {
		return ""
	}
	name := strings.TrimSpace(a.Name)
	addr := strings.TrimSpace(a.Address)
	switch {
	case name == "":
		return addr
	case addr == "":
		return name
	default:
		return name + " <" + addr + ">"
	}
}

// addrStrings flattens a recipient list (empty entries dropped).
func addrStrings(in []index.Address) []string {
	out := make([]string, 0, len(in))
	for i := range in {
		if s := addrString(&in[i]); s != "" {
			out = append(out, s)
		}
	}
	return out
}
