package rules

import (
	"strings"
)

// NeedsHint is what sync must fetch because a CEL expression references it (W73 §3).
type NeedsHint struct {
	Text        bool
	Attachments bool
	Headers     []string // lower-case; may include "*"
	Labels      bool
}

// NeedsFromExpr inspects filter and map source for helper / field references.
func NeedsFromExpr(filter, mapExpr string) NeedsHint {
	var n NeedsHint
	scanSrc(&n, filter)
	scanSrc(&n, mapExpr)
	n.Headers = uniqLower(n.Headers)
	return n
}

func scanSrc(n *NeedsHint, src string) {
	src = strings.TrimSpace(src)
	if src == "" {
		return
	}
	lower := strings.ToLower(src)
	checks := []struct {
		needle string
		fn     func(*NeedsHint)
	}{
		{"text(", func(h *NeedsHint) { h.Text = true }},
		{"textcontains(", func(h *NeedsHint) { h.Text = true }},
		{"textmatches(", func(h *NeedsHint) { h.Text = true }},
		{"hasattachments(", func(h *NeedsHint) { h.Attachments = true }},
		{"attachments(", func(h *NeedsHint) { h.Attachments = true }},
		{"hasattachment(", func(h *NeedsHint) { h.Attachments = true }},
		{"attachmenttypes(", func(h *NeedsHint) { h.Attachments = true }},
		{"isautoreply(", func(h *NeedsHint) {
			h.Headers = append(h.Headers, "auto-submitted", "x-autoreply", "x-autorespond", "precedence")
		}},
		{"isbounce(", func(h *NeedsHint) {
			h.Headers = append(h.Headers, "x-failed-recipients", "content-type")
		}},
		{"isreply(", func(h *NeedsHint) {
			h.Headers = append(h.Headers, "in-reply-to", "references")
		}},
		{"labels(", func(h *NeedsHint) { h.Labels = true }},
		{"haslabel(", func(h *NeedsHint) { h.Labels = true }},
		{"isinbox(", func(h *NeedsHint) { h.Labels = true }},
		{"data.text", func(h *NeedsHint) { h.Text = true }},
		{"data.attachments", func(h *NeedsHint) { h.Attachments = true }},
	}
	for _, c := range checks {
		if strings.Contains(lower, c.needle) {
			c.fn(n)
		}
	}
	n.Headers = append(n.Headers, dataHeaderRefs(lower)...)
	for _, fn := range []string{"header(", "hasheader(", "headervalues("} {
		n.Headers = append(n.Headers, headerCallRefs(lower, fn)...)
	}
}

const blanks = " \t\r\n"

// headerCallRefs returns the header names of header("x") / hasHeader("x") / headerValues("x") calls with a
// string literal argument; a computed argument needs all headers ("*").
func headerCallRefs(lower, fn string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(lower[i:], fn)
		if j < 0 {
			return out
		}
		start := i + j
		i = start + len(fn)
		if start > 0 && (isIdentByte(lower[start-1]) || lower[start-1] == '.') {
			continue // e.g. hasheader( inside header( scan, or a method named like it
		}
		rest := strings.TrimLeft(lower[i:], blanks)
		if len(rest) > 1 && (rest[0] == '"' || rest[0] == '\'') {
			if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
				after := strings.TrimLeft(rest[2+end:], blanks)
				if strings.HasPrefix(after, ")") {
					out = append(out, rest[1:1+end])
					continue
				}
			}
		}
		out = append(out, "*")
	}
}

// dataHeaderRefs returns the header names a `data.headers` reference needs: `data.headers["x-a"]` or
// `data.headers.x_a` → that header; `data.headers` used as a whole (e.g. `"headers": data.headers`, or passed
// to a function) → "*" (all headers). lower is the lower-cased source.
func dataHeaderRefs(lower string) []string {
	const needle = "data.headers"
	var out []string
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return out
		}
		pos := i + j + len(needle)
		i = pos
		if pos < len(lower) && isIdentByte(lower[pos]) {
			continue // e.g. data.headersFoo — not our field
		}
		rest := strings.TrimLeft(lower[pos:], blanks)
		switch {
		case strings.HasPrefix(rest, "[\"") || strings.HasPrefix(rest, "['"):
			q := rest[1]
			if end := strings.IndexByte(rest[2:], q); end >= 0 {
				out = append(out, rest[2:2+end])
				continue
			}
			out = append(out, "*")
		case strings.HasPrefix(rest, "."):
			k := 1
			for k < len(rest) && isIdentByte(rest[k]) {
				k++
			}
			if k > 1 {
				out = append(out, rest[1:k])
				continue
			}
			out = append(out, "*")
		default:
			out = append(out, "*")
		}
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func uniqLower(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	star := false
	for _, h := range in {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if h == "*" {
			star = true
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	if star {
		return []string{"*"}
	}
	return out
}

// MergeNeedsHint folds a hint into webhook-style needs fields.
func (n NeedsHint) Empty() bool {
	return !n.Text && !n.Attachments && len(n.Headers) == 0 && !n.Labels
}
