// Package i18n holds the product's supported UI / system-mail locale codes.
package i18n

import (
	"strconv"
	"strings"
)

// Supported is the fixed set of short locale codes (W60).
var Supported = []string{"en", "it", "es", "fr", "de", "pt", "ru", "zh", "ja"}

var supportedSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Supported))
	for _, c := range Supported {
		m[c] = struct{}{}
	}
	return m
}()

// IsSupported reports whether code is one of the nine product locales.
func IsSupported(code string) bool {
	_, ok := supportedSet[strings.ToLower(strings.TrimSpace(code))]
	return ok
}

// HasSupportedPrimary reports whether tag's primary language subtag is one of
// the nine product locales (BCP 47; case-insensitive; '_' tolerated).
func HasSupportedPrimary(tag string) bool {
	return IsSupported(primaryOf(tag))
}

// Normalize maps any BCP 47 / Accept-Language style tag to a supported short
// code by primary subtag. Unknown or empty input becomes "en".
func Normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return "en"
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = strings.TrimSpace(tag[:i])
	}
	if i := strings.IndexByte(tag, ';'); i >= 0 {
		tag = strings.TrimSpace(tag[:i])
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	primary := tag
	if i := strings.IndexByte(tag, '-'); i >= 0 {
		primary = tag[:i]
	}
	if _, ok := supportedSet[primary]; ok {
		return primary
	}
	return "en"
}

// FromAcceptLanguage picks the best supported locale from an Accept-Language header.
// Tags whose primary subtag is not supported are skipped; if none match, returns "en".
func FromAcceptLanguage(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return "en"
	}
	bestCode := ""
	bestQ := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if i := strings.IndexByte(part, ';'); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			rest := strings.ToLower(strings.TrimSpace(part[i+1:]))
			if strings.HasPrefix(rest, "q=") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(rest[2:]), 64); err == nil {
					q = v
				}
			}
		}
		primary := primaryOf(tag)
		if !IsSupported(primary) {
			continue
		}
		if q > bestQ {
			bestQ = q
			bestCode = primary
		}
	}
	if bestCode == "" {
		return "en"
	}
	return bestCode
}

func primaryOf(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	if i := strings.IndexByte(tag, ';'); i >= 0 {
		tag = strings.TrimSpace(tag[:i])
	}
	if i := strings.IndexByte(tag, '-'); i >= 0 {
		return tag[:i]
	}
	return tag
}
