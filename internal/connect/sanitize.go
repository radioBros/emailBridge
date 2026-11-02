package connect

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
)

const (
	maxHeaderHTML = 8 << 10
	maxHeadHTML   = 4 << 10
)

var (
	headerOnce sync.Once
	headerPol  *bluemonday.Policy
	headOnce   sync.Once
	headPol    *bluemonday.Policy

	cssImportRE = regexp.MustCompile(`(?i)@import\b[^;]*;`)
	cssDangerRE = regexp.MustCompile(`(?i)(expression\s*\(|-moz-binding|behavior\s*:|javascript\s*:)`)
	metaHTTPEq  = regexp.MustCompile(`(?i)^content-language$|^x-ua-compatible$`)
)

func headerPolicy() *bluemonday.Policy {
	headerOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements(
			"p", "div", "span", "br", "hr",
			"h1", "h2", "h3",
			"a", "img",
			"strong", "em", "b", "i", "u",
			"ul", "ol", "li",
		)
		p.AllowAttrs("href", "title").OnElements("a")
		p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
		p.RequireParseableURLs(true)
		p.AllowURLSchemes("http", "https", "mailto")
		p.RequireNoReferrerOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		headerPol = p
	})
	return headerPol
}

func headPolicy() *bluemonday.Policy {
	headOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements("meta", "link", "style")
		p.AllowAttrs("name", "charset", "property", "content", "http-equiv").OnElements("meta")
		p.AllowAttrs("rel", "href", "type", "media", "crossorigin").OnElements("link")
		p.AllowAttrs("media", "type").OnElements("style")
		p.RequireParseableURLs(true)
		p.AllowURLSchemes("https")
		headPol = p
	})
	return headPol
}

// SanitizeHeaderHTML returns XSS-safe body HTML for the hosted connect header.
func SanitizeHeaderHTML(raw string) string {
	raw = truncateUTF8(raw, maxHeaderHTML)
	out := headerPolicy().Sanitize(raw)
	return filterImgSrc(out)
}

// SanitizeHeadHTML returns XSS-safe head fragments (meta / https stylesheet / style).
func SanitizeHeadHTML(raw string) string {
	raw = truncateUTF8(raw, maxHeadHTML)
	out := headPolicy().Sanitize(raw)
	return scrubHeadFragment(out)
}

// BrandingPreview is the sanitised preview response.
type BrandingPreview struct {
	HeaderHTML string `json:"headerHtml,omitempty" doc:"The header HTML as it would be stored." example:"<div><p>Connect to <b>Acme</b></p></div>"`
	HeadHTML   string `json:"headHtml,omitempty" doc:"The head HTML as it would be stored." example:"<style> .brand{color:#1D4ED8}</style>"`
}

// PreviewBranding sanitises header/head HTML without persisting.
func PreviewBranding(header, head string) BrandingPreview {
	return BrandingPreview{
		HeaderHTML: SanitizeHeaderHTML(header),
		HeadHTML:   SanitizeHeadHTML(head),
	}
}

// StyleHashes returns CSP sha256 hashes for each <style> body in sanitised head HTML.
func StyleHashes(headHTML string) []string {
	var out []string
	lower := strings.ToLower(headHTML)
	for {
		start := strings.Index(lower, "<style")
		if start < 0 {
			break
		}
		gt := strings.Index(lower[start:], ">")
		if gt < 0 {
			break
		}
		bodyStart := start + gt + 1
		end := strings.Index(lower[bodyStart:], "</style>")
		if end < 0 {
			break
		}
		body := headHTML[bodyStart : bodyStart+end]
		sum := sha256.Sum256([]byte(body))
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		next := bodyStart + end + len("</style>")
		headHTML = headHTML[next:]
		lower = strings.ToLower(headHTML)
	}
	return out
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

func filterImgSrc(html string) string {
	// Drop non-https / non-path image sources left after policy (http allowed for anchors only).
	// Relative paths including /v1/public/branding/assets/{id} are kept (W68).
	re := regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc\s*=\s*)(["'])([^"']*)(["'])`)
	return re.ReplaceAllStringFunc(html, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if len(sub) != 5 {
			return ""
		}
		src := strings.TrimSpace(sub[3])
		if strings.HasPrefix(src, "/") {
			return m
		}
		u, err := url.Parse(src)
		if err != nil || !strings.EqualFold(u.Scheme, "https") {
			return ""
		}
		return m
	})
}

func scrubHeadFragment(html string) string {
	html = dropBadMeta(html)
	html = dropBadLinks(html)
	html = scrubStyles(html)
	return html
}

func dropBadMeta(html string) string {
	re := regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	return re.ReplaceAllStringFunc(html, func(tag string) string {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, "http-equiv") {
			eq := attrValue(tag, "http-equiv")
			if !metaHTTPEq.MatchString(eq) {
				return ""
			}
		}
		return tag
	})
}

func dropBadLinks(html string) string {
	re := regexp.MustCompile(`(?is)<link\b[^>]*>`)
	return re.ReplaceAllStringFunc(html, func(tag string) string {
		rel := strings.ToLower(strings.TrimSpace(attrValue(tag, "rel")))
		if rel != "stylesheet" {
			return ""
		}
		href := strings.TrimSpace(attrValue(tag, "href"))
		u, err := url.Parse(href)
		if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
			return ""
		}
		return tag
	})
}

func scrubStyles(html string) string {
	re := regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	return re.ReplaceAllStringFunc(html, func(block string) string {
		openEnd := strings.Index(block, ">")
		close := strings.LastIndex(strings.ToLower(block), "</style>")
		if openEnd < 0 || close < 0 || close <= openEnd {
			return ""
		}
		body := block[openEnd+1 : close]
		body = cssImportRE.ReplaceAllStringFunc(body, func(imp string) string {
			if strings.Contains(strings.ToLower(imp), "https:") {
				return imp
			}
			return "/*stripped-import*/"
		})
		if cssDangerRE.MatchString(body) {
			body = cssDangerRE.ReplaceAllString(body, "/*stripped*/")
		}
		return block[:openEnd+1] + body + "</style>"
	})
}

func attrValue(tag, name string) string {
	lower := strings.ToLower(tag)
	key := strings.ToLower(name) + "="
	idx := strings.Index(lower, key)
	if idx < 0 {
		key = strings.ToLower(name) + " ="
		idx = strings.Index(lower, key)
		if idx < 0 {
			return ""
		}
	}
	rest := tag[idx+len(key):]
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		q := rest[0]
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return rest[1:]
		}
		return rest[1 : end+1]
	}
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '\t' || r == '>' {
			end = i
			break
		}
	}
	return rest[:end]
}
