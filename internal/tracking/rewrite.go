package tracking

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// PrepareInput is the build-path rewrite request.
type PrepareInput struct {
	OrgID, AccountID, OutboundID string
	Recipient                    string
	HTML                         string
	Headers                      map[string]string
	TrackOpens, TrackClicks      bool
	ListUnsub                    bool
	// ListID is the RFC 2919 List-Id token (without angle brackets); sets List-Id when absent (W53).
	ListID    string
	PublicURL string
	ExpireAt  time.Time
}

// Link is one rewritten click target to persist.
type Link struct {
	OrganizationID string
	OutboundID     string
	Idx            int
	URL            string
	Hash           string
	ExpireAt       time.Time
}

// PrepareResult is rewritten HTML/headers plus links to store.
type PrepareResult struct {
	HTML    string
	Headers map[string]string
	Links   []Link
}

var hrefRE = regexp.MustCompile(`(?i)(<a\b[^>]*\bhref\s*=\s*)(["'])([^"']+)(["'])`)

// Prepare rewrites HTML and injects List-Unsubscribe headers when switches are on.
func Prepare(key []byte, in PrepareInput) (*PrepareResult, error) {
	out := &PrepareResult{
		HTML:    in.HTML,
		Headers: copyHeaders(in.Headers),
	}
	base := strings.TrimRight(in.PublicURL, "/")
	if (in.TrackOpens || in.TrackClicks || in.ListUnsub) && base == "" {
		return nil, fmt.Errorf("tracking: public URL required")
	}
	// List-Id alone does not require a public URL.
	exp := in.ExpireAt
	if exp.IsZero() {
		exp = time.Now().UTC().Add(defaultTTL)
	}
	rcpt := strings.ToLower(strings.TrimSpace(in.Recipient))

	if in.TrackClicks && in.HTML != "" {
		idx := 0
		out.HTML = hrefRE.ReplaceAllStringFunc(in.HTML, func(m string) string {
			sub := hrefRE.FindStringSubmatch(m)
			if len(sub) != 5 {
				return m
			}
			raw := sub[3]
			if !trackableURL(raw) {
				return m
			}
			tok, err := Sign(key, Claims{
				Kind: KindClick, OrgID: in.OrgID, OutboundID: in.OutboundID,
				Recipient: rcpt, Index: idx, Exp: exp,
			})
			if err != nil {
				return m
			}
			h := sha256.Sum256([]byte(raw))
			out.Links = append(out.Links, Link{
				OrganizationID: in.OrgID, OutboundID: in.OutboundID, Idx: idx,
				URL: raw, Hash: hex.EncodeToString(h[:]), ExpireAt: exp,
			})
			tracked := base + "/t/c/" + tok
			idx++
			return sub[1] + sub[2] + tracked + sub[4]
		})
	}

	if in.TrackOpens && out.HTML != "" {
		tok, err := Sign(key, Claims{
			Kind: KindOpen, OrgID: in.OrgID, OutboundID: in.OutboundID,
			Recipient: rcpt, Exp: exp,
		})
		if err == nil {
			pixel := `<img src="` + base + `/t/o/` + tok + `" width="1" height="1" alt="" style="display:none!important;border:0;width:1px;height:1px" />`
			lower := strings.ToLower(out.HTML)
			if i := strings.LastIndex(lower, "</body>"); i >= 0 {
				out.HTML = out.HTML[:i] + pixel + out.HTML[i:]
			} else {
				out.HTML += pixel
			}
		}
	}

	if in.ListUnsub {
		if _, ok := headerGet(out.Headers, "List-Unsubscribe"); !ok {
			tok, err := Sign(key, Claims{
				Kind: KindUnsub, OrgID: in.OrgID, OutboundID: in.OutboundID,
				Recipient: rcpt, Exp: exp,
			})
			if err == nil {
				out.Headers["List-Unsubscribe"] = "<" + base + "/u/" + tok + ">"
				if _, ok := headerGet(out.Headers, "List-Unsubscribe-Post"); !ok {
					out.Headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
				}
			}
		}
	}
	if lid := strings.TrimSpace(in.ListID); lid != "" {
		if _, ok := headerGet(out.Headers, "List-Id"); !ok {
			out.Headers["List-Id"] = "<" + lid + ">"
		}
	}
	return out, nil
}

func trackableURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return false
	}
	lower := strings.ToLower(raw)
	for _, p := range []string{"mailto:", "cid:", "data:", "javascript:", "tel:"} {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	if strings.Contains(lower, "/t/c/") || strings.Contains(lower, "/t/o/") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func copyHeaders(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func headerGet(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
