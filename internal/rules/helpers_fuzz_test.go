package rules

import (
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
)

func FuzzHasAttachment(f *testing.F) {
	f.Add("*.pdf", "report.pdf")
	f.Add("/inv.*/", "invoice-1.pdf")
	f.Add("???.txt", "ab.txt")
	f.Fuzz(func(t *testing.T, pat, name string) {
		setEvalBag(map[string]any{
			"attachments": []any{map[string]any{"filename": name, "contentType": "x", "size": 1}},
		}, nil, time.Now().UTC(), 0)
		_ = hasAttachmentFn(types.String(pat))
	})
}

func FuzzTextMatches(f *testing.F) {
	f.Add("hello", "hel+")
	f.Fuzz(func(t *testing.T, body, pat string) {
		setEvalBag(map[string]any{"text": map[string]any{"plain": body}}, nil, time.Now().UTC(), 0)
		_ = textMatchesFn(types.String(pat))
	})
}

func FuzzBusinessHours(f *testing.F) {
	f.Add("UTC", "09:00-18:00", "Mon-Fri")
	f.Add("Europe/Rome", "08:30-17:30", "Mon,Wed,Fri")
	f.Fuzz(func(t *testing.T, tz, window, days string) {
		setEvalBag(nil, nil, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), 0)
		_ = businessHoursFn(types.String(tz), types.String(window), types.String(days))
	})
}

// FuzzNeedsFromExpr: the needs scanner runs on the sync path (payload-needs resolver) over any stored
// expression, so it must never panic and only ever return lower-case header names.
func FuzzNeedsFromExpr(f *testing.F) {
	for _, seed := range []string{
		`{"headers": data.headers}`, `data.headers["x-a"] == ["1"]`, `has(data.headers.priority)`,
		`header("X-Prio") == "1" && hasHeader('list-id')`, `headerValues(data.subject)`, `data.headers[`,
		`header("`, `data.headers.`, `data.headers['`, `hasheader(`, `textContains("a") && hasAttachment("*.pdf")`,
	} {
		f.Add(seed, `{"v": data.headers}`)
	}
	f.Fuzz(func(t *testing.T, filter, mapExpr string) {
		n := NeedsFromExpr(filter, mapExpr)
		for _, h := range n.Headers {
			if h == "" || h != strings.ToLower(h) {
				t.Fatalf("header %q from %q / %q", h, filter, mapExpr)
			}
		}
	})
}
