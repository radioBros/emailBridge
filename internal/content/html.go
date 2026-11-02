package content

import (
	"bytes"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// RemoteImages controls how http(s) image URLs are handled before sanitising.
type RemoteImages int

const (
	// RemoteBlock drops remote image sources (default; no tracking pixels).
	RemoteBlock RemoteImages = iota
	// RemoteAllow keeps remote http(s) image sources for the sanitiser.
	RemoteAllow
	// RemoteProxy replaces remote sources via HTMLOptions.ProxyURL.
	RemoteProxy
)

// DefaultHTMLMax is the default SanitizeHTML input cap (EB_SYNC_TEXT_MAX / 256 KiB).
const DefaultHTMLMax = 256 << 10

// HTMLOptions configures SanitizeHTML.
type HTMLOptions struct {
	MaxBytes     int                     // ≤0 → DefaultHTMLMax
	MapCID       func(cid string) string // cid → attachment URL; "" clears src
	RemoteImages RemoteImages
	ProxyURL     func(remote string) string // used when RemoteImages == RemoteProxy
}

// SanitizeResult is the output of SanitizeHTML.
type SanitizeResult struct {
	HTML      string
	Truncated bool
}

var (
	policyOnce sync.Once
	mailPolicy *bluemonday.Policy
)

// mailHTMLPolicy builds the explicit allowlist (P4.2 §2.2) — not UGCPolicy.
func mailHTMLPolicy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements(
			"a", "p", "div", "span", "br", "hr",
			"h1", "h2", "h3", "h4", "h5", "h6",
			"blockquote", "pre", "code",
			"ul", "ol", "li",
			"table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption",
			"em", "strong", "b", "i", "u", "s", "sub", "sup",
			"img", "figure", "figcaption",
		)
		p.AllowAttrs("href", "title").OnElements("a")
		p.AllowAttrs("src", "alt", "title").OnElements("img")
		p.AllowAttrs("width", "height").Matching(bluemonday.Integer).OnElements("img", "td", "th")
		p.AllowAttrs("colspan", "rowspan").Matching(bluemonday.Integer).OnElements("td", "th")
		p.AllowAttrs("align").Matching(bluemonday.CellAlign).OnElements("td", "th", "tr", "table")
		p.AllowAttrs("valign").Matching(bluemonday.CellVerticalAlign).OnElements("td", "th")
		p.RequireParseableURLs(true)
		p.AllowURLSchemes("http", "https", "mailto")
		p.RequireNoReferrerOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		mailPolicy = p
	})
	return mailPolicy
}

// SanitizeHTML returns XSS-safe HTML for payloads and admin iframes.
func SanitizeHTML(raw string, opts HTMLOptions) SanitizeResult {
	max := opts.MaxBytes
	if max <= 0 {
		max = DefaultHTMLMax
	}
	truncated := false
	if len(raw) > max {
		raw = truncateUTF8(raw, max)
		truncated = true
	}
	pre := rewriteImageURLs(raw, opts)
	out := mailHTMLPolicy().Sanitize(pre)
	return SanitizeResult{HTML: out, Truncated: truncated}
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func rewriteImageURLs(raw string, opts HTMLOptions) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "img", "source":
				n.Attr = mapAttrs(n.Attr, opts)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	body := findBody(doc)
	if body == nil {
		var buf bytes.Buffer
		if err := html.Render(&buf, doc); err != nil {
			return raw
		}
		return buf.String()
	}
	var buf bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&buf, c); err != nil {
			return raw
		}
	}
	return buf.String()
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "body") {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

func mapAttrs(attrs []html.Attribute, opts HTMLOptions) []html.Attribute {
	out := make([]html.Attribute, 0, len(attrs))
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		switch key {
		case "src":
			a.Val = mapOneURL(a.Val, opts)
			if a.Val == "" {
				continue
			}
		case "srcset":
			// keep only the first URL candidate, mapped into a plain value we may drop
			first := strings.Fields(a.Val)
			if len(first) == 0 {
				continue
			}
			mapped := mapOneURL(first[0], opts)
			if mapped == "" {
				continue
			}
			// promote to src-like single URL; drop srcset from allowlist later
			out = append(out, html.Attribute{Key: "src", Val: mapped})
			continue
		}
		out = append(out, a)
	}
	return out
}

func mapOneURL(raw string, opts HTMLOptions) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "cid:") {
		cid := strings.TrimSpace(raw[4:])
		cid = strings.Trim(cid, "<>")
		if opts.MapCID == nil {
			return ""
		}
		return strings.TrimSpace(opts.MapCID(cid))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	isRemote := scheme == "http" || scheme == "https" || (scheme == "" && strings.HasPrefix(raw, "//"))
	if !isRemote {
		return raw // leave mailto etc. — sanitiser decides
	}
	abs := raw
	if strings.HasPrefix(raw, "//") {
		abs = "https:" + raw
	}
	switch opts.RemoteImages {
	case RemoteAllow:
		return raw
	case RemoteProxy:
		if opts.ProxyURL == nil {
			return ""
		}
		return strings.TrimSpace(opts.ProxyURL(abs))
	default:
		return ""
	}
}
