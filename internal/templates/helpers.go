package templates

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	hbs "github.com/radiobros/emailbridge/internal/templates/hbs"
)

// registerBuiltinHelpers installs date / default / upper / lower / url on tpl.
func registerBuiltinHelpers(tpl *hbs.Template, locale, tz string) {
	loc := loadLocation(tz)
	tpl.RegisterHelper("date", func(value any, options *hbs.Options) string {
		format := "2006-01-02 15:04:05"
		here := loc
		if options != nil {
			if f := options.HashStr("format"); f != "" {
				format = f
			} else if p := options.ParamStr(0); p != "" {
				format = p
			}
			if t := options.HashStr("tz"); t != "" {
				here = loadLocation(t)
			}
		}
		t, ok := asTime(value)
		if !ok {
			return ""
		}
		return t.In(here).Format(format)
	})
	tpl.RegisterHelper("default", func(value, fallback any) string {
		if isEmpty(value) {
			return hbs.Str(fallback)
		}
		return hbs.Str(value)
	})
	tpl.RegisterHelper("upper", func(value any) string {
		return strings.ToUpper(fmt.Sprint(value))
	})
	tpl.RegisterHelper("lower", func(value any) string {
		return strings.ToLower(fmt.Sprint(value))
	})
	tpl.RegisterHelper("url", func(value any) hbs.SafeString {
		s := strings.TrimSpace(fmt.Sprint(value))
		if s == "" || s == "<nil>" {
			return ""
		}
		if !safeTemplateURL(s) {
			panic(fmt.Errorf("unsafe URL"))
		}
		return hbs.SafeString(url.QueryEscape(s))
	})
	_ = locale
}

// safeTemplateURL is an allow-list for the {{url}} helper (W35): http(s), mailto, tel, or
// scheme-less relative; refuses javascript/vbscript/file/data tricks and protocol-relative //.
func safeTemplateURL(s string) bool {
	if s == "" || strings.ContainsAny(s, "\r\n\x00\t") {
		return false
	}
	trimmed := strings.TrimSpace(s)
	if trimmed != s {
		return false // leading/trailing whitespace before a scheme
	}
	return safeTemplateURLNorm(strings.ToLower(trimmed)) &&
		safeTemplateURLNorm(strings.ToLower(decodeURLObfuscation(trimmed)))
}

func decodeURLObfuscation(s string) string {
	r := strings.NewReplacer(
		"&#58;", ":", "&#x3a;", ":", "&#x3A;", ":",
		"&colon;", ":", "&COLON;", ":",
		"%3a", ":", "%3A", ":",
	)
	return r.Replace(s)
}

func safeTemplateURLNorm(lower string) bool {
	if strings.HasPrefix(lower, "//") {
		return false
	}
	if i := strings.IndexByte(lower, ':'); i >= 0 {
		scheme := lower[:i]
		if scheme == "" || strings.ContainsAny(scheme, " \t\r\n\x00") {
			return false
		}
		switch scheme {
		case "http", "https", "mailto", "tel":
			return true
		default:
			return false
		}
	}
	return true
}

func loadLocation(tz string) *time.Location {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	case float64:
		return time.Unix(int64(t), 0).UTC(), true
	case int64:
		return time.Unix(t, 0).UTC(), true
	case int:
		return time.Unix(int64(t), 0).UTC(), true
	}
	return time.Time{}, false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case bool:
		return !x
	}
	s := fmt.Sprint(v)
	return s == "" || s == "<nil>"
}
