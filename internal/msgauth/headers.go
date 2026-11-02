package msgauth

import (
	"net/textproto"
	"strings"

	"github.com/emersion/go-msgauth/authres"
)

// Headers is a textproto-like multi-value header map (MIMEHeader, map adapter, …).
type Headers interface {
	Values(key string) []string
}

// MapHeaders adapts map[string][]string (keys compared case-insensitively via MIME canonicalisation).
type MapHeaders map[string][]string

// Values implements Headers.
func (m MapHeaders) Values(key string) []string {
	if m == nil {
		return nil
	}
	if v, ok := m[textproto.CanonicalMIMEHeaderKey(key)]; ok {
		return v
	}
	// Fall back to a case-insensitive scan for non-canonical maps.
	want := strings.ToLower(key)
	for k, v := range m {
		if strings.EqualFold(k, want) || strings.ToLower(k) == want {
			return v
		}
	}
	return nil
}

// FromHeaders parses Authentication-Results headers, keeping only those whose authserv-id is in
// trusted (case-insensitive). The topmost trusted header wins. Source is "header".
func FromHeaders(h Headers, trusted []string) (*Results, error) {
	out := emptyResults(SourceHeader)
	if h == nil || len(trusted) == 0 {
		return out, nil
	}
	trust := make(map[string]struct{}, len(trusted))
	for _, t := range trusted {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			trust[t] = struct{}{}
		}
	}
	if len(trust) == 0 {
		return out, nil
	}

	vals := h.Values("Authentication-Results")
	for _, raw := range vals {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, results, err := authres.Parse(raw)
		if err != nil {
			continue // skip malformed headers; try the next
		}
		id = strings.TrimSpace(strings.ToLower(id))
		if _, ok := trust[id]; !ok {
			continue
		}
		fillFromAuthRes(out, raw, results)
		return out, nil
	}
	return out, nil
}

func fillFromAuthRes(out *Results, raw string, results []authres.Result) {
	selectors := dkimSelectorsFromRaw(raw)
	dkimIdx := 0
	for _, r := range results {
		switch v := r.(type) {
		case *authres.SPFResult:
			out.SPF = Item{
				Result: normalizeResult(string(v.Value)),
				Domain: spfDomain(v),
			}
		case *authres.DKIMResult:
			item := Item{
				Result: normalizeResult(string(v.Value)),
				Domain: v.Domain,
			}
			if dkimIdx < len(selectors) {
				item.Selector = selectors[dkimIdx]
			}
			dkimIdx++
			out.DKIM = append(out.DKIM, item)
		case *authres.DMARCResult:
			out.DMARC = Item{
				Result: normalizeResult(string(v.Value)),
				Domain: v.From,
			}
		case *authres.ARCResult:
			out.ARC = Item{Result: normalizeResult(string(v.Value))}
		}
	}
}

func spfDomain(r *authres.SPFResult) string {
	if r.From != "" {
		if i := strings.LastIndex(r.From, "@"); i >= 0 && i+1 < len(r.From) {
			return strings.ToLower(r.From[i+1:])
		}
		return strings.ToLower(r.From)
	}
	return strings.ToLower(r.Helo)
}

// dkimSelectorsFromRaw walks Authentication-Results sections and collects header.s for each dkim method.
func dkimSelectorsFromRaw(raw string) []string {
	parts := strings.Split(raw, ";")
	var out []string
	for i := 1; i < len(parts); i++ {
		s := strings.TrimSpace(parts[i])
		if s == "" {
			continue
		}
		fields := strings.Fields(s)
		if len(fields) == 0 {
			continue
		}
		method, _, ok := strings.Cut(fields[0], "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(method), "dkim") {
			continue
		}
		sel := ""
		for _, f := range fields[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(k), "header.s") {
				sel = strings.TrimSpace(v)
				break
			}
		}
		out = append(out, sel)
	}
	return out
}
