package rules

import (
	"html"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// evalBag holds per-Eval context for custom helpers (evalMu serialises access).
type evalBag struct {
	data      map[string]any
	headers   map[string][]string
	lists     map[string]map[string]struct{} // name → lowercased membership set
	eventTime time.Time
	eventType string
	textMax   int
}

var evalBagStore sync.Map // key "bag" → *evalBag

// evalMu serialises every evaluation in the process: the custom helpers read the per-evaluation context
// (data, headers, named lists, event time) from the process-wide bag above, so two evaluations — the
// router and an API /test or dry-run job on the same node, of any engine — must never interleave
// (cross-tenant safety). Evaluations are microseconds and bounded by the engine time limit.
var evalMu sync.Mutex

const defaultTextMax = 64 << 10

func setEvalBag(data map[string]any, lists map[string]map[string]struct{}, eventTime time.Time, textMax int) {
	setEvalBagTyped("", data, lists, eventTime, textMax)
}

// setEvalBagTyped is setEvalBag with the event type (isBounce() recognises message.bounced).
func setEvalBagTyped(eventType string, data map[string]any, lists map[string]map[string]struct{}, eventTime time.Time, textMax int) {
	if textMax <= 0 {
		textMax = defaultTextMax
	}
	h := map[string][]string{}
	if data != nil {
		raw, ok := data["headers"]
		if ok && raw != nil {
			switch v := raw.(type) {
			case map[string][]string:
				for k, vv := range v {
					h[strings.ToLower(k)] = vv
				}
			case map[string]any:
				for k, vv := range v {
					switch s := vv.(type) {
					case []string:
						h[strings.ToLower(k)] = s
					case []any:
						out := make([]string, 0, len(s))
						for _, x := range s {
							if ss, ok := x.(string); ok {
								out = append(out, ss)
							}
						}
						h[strings.ToLower(k)] = out
					}
				}
			}
		}
	}
	if lists == nil {
		lists = map[string]map[string]struct{}{}
	}
	if data == nil {
		data = map[string]any{}
	}
	bag := &evalBag{data: data, headers: h, lists: lists, eventTime: eventTime, eventType: eventType, textMax: textMax}
	evalBagStore.Store("bag", bag)
	evalHeaders.Store("h", headerBag{m: h})
}

func getBag() *evalBag {
	v, ok := evalBagStore.Load("bag")
	if !ok {
		return &evalBag{data: map[string]any{}, headers: map[string][]string{}, lists: map[string]map[string]struct{}{}, textMax: defaultTextMax}
	}
	return v.(*evalBag)
}

func getData() map[string]any { return getBag().data }

func stringField(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok || mm == nil {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

func labelsFromData(data map[string]any) []string {
	var out []string
	add := func(v any) {
		switch x := v.(type) {
		case []string:
			out = append(out, x...)
		case []any:
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	add(data["labels"])
	add(data["flags"])
	if gmail, ok := data["gmail"].(map[string]any); ok {
		add(gmail["labels"])
		add(gmail["labelIds"])
	}
	return out
}

func stripHTML(s string) string {
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<[^>]+>`)
	s = re.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func capRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	// byte-cap then trim incomplete rune
	s = s[:max]
	for i := 0; i < utf8.UTFMax-1 && len(s) > 0; i++ { // drop a rune cut in half (at most 3 bytes)
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func firstAddress(v any) string {
	switch a := v.(type) {
	case string:
		return a
	case map[string]any:
		return addressString(a)
	case []any:
		if len(a) > 0 {
			return firstAddress(a[0])
		}
	case []string:
		if len(a) > 0 {
			return a[0]
		}
	}
	return ""
}

func addressList(v any) []string {
	switch a := v.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), a...)
	case []any:
		out := make([]string, 0, len(a))
		for _, e := range a {
			if s := firstAddress(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		if s := addressString(a); s != "" {
			return []string{s}
		}
	case string:
		if a != "" {
			return []string{a}
		}
	}
	return nil
}

func parseEventTime(event, data map[string]any, fallback time.Time) time.Time {
	for _, src := range []any{event["time"], data["date"], data["internalDate"]} {
		switch t := src.(type) {
		case time.Time:
			if !t.IsZero() {
				return t.UTC()
			}
		case string:
			if parsed, err := time.Parse(time.RFC3339, t); err == nil {
				return parsed.UTC()
			}
		}
	}
	if fallback.IsZero() {
		return time.Now().UTC()
	}
	return fallback.UTC()
}

func matchGlobOrRe2(pat, name string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return false
	}
	if len(pat) >= 2 && pat[0] == '/' && pat[len(pat)-1] == '/' {
		re, err := regexp.Compile(pat[1 : len(pat)-1])
		if err != nil {
			return false
		}
		return re.MatchString(name)
	}
	return matchGlob(pat, name)
}

func matchGlob(pat, s string) bool {
	// simple * and ? glob, case-insensitive
	pat = strings.ToLower(pat)
	s = strings.ToLower(s)
	var rx strings.Builder
	rx.WriteString("^")
	for _, r := range pat {
		switch r {
		case '*':
			rx.WriteString(".*")
		case '?':
			rx.WriteString(".")
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
			rx.WriteRune('\\')
			rx.WriteRune(r)
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
				rx.WriteRune(r)
			} else {
				rx.WriteRune('\\')
				rx.WriteRune(r)
			}
		}
	}
	rx.WriteString("$")
	re, err := regexp.Compile(rx.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}
