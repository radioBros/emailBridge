package config

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Size is a byte count. Values accept plain integers (bytes) or binary units:
// KiB, MiB, GiB (e.g. "25MiB", "4KiB").
type Size int64

// String returns a compact human form.
func (s Size) String() string {
	v := int64(s)
	switch {
	case v == 0:
		return "0"
	case v%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", v>>30)
	case v%(1<<20) == 0:
		return fmt.Sprintf("%dMiB", v>>20)
	case v%(1<<10) == 0:
		return fmt.Sprintf("%dKiB", v>>10)
	default:
		return strconv.FormatInt(v, 10)
	}
}

// ParseSize parses a Size from a config string.
func ParseSize(s string) (Size, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	units := []struct {
		suf string
		mul int64
	}{
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			n, err := strconv.ParseInt(strings.TrimSpace(s[:len(s)-len(u.suf)]), 10, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return Size(n * u.mul), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (want integer bytes or KiB/MiB/GiB)", s)
	}
	return Size(n), nil
}

// Rate is a token-bucket rate limit.
//
// Syntax: `<count>/<unit>` where unit is `s`, `min`, or `h`, with an optional
// `,burst=<n>` (default burst = count). Examples: `20/s`, `10/min`, `100/h`,
// `20/s,burst=40`.
type Rate struct {
	Count  int           // tokens per Interval
	Period time.Duration // s, min, or h
	Burst  int           // max burst; defaults to Count when omitted
}

// String formats the rate for display.
func (r Rate) String() string {
	unit := "s"
	switch r.Period {
	case time.Minute:
		unit = "min"
	case time.Hour:
		unit = "h"
	}
	s := fmt.Sprintf("%d/%s", r.Count, unit)
	if r.Burst != 0 && r.Burst != r.Count {
		s += fmt.Sprintf(",burst=%d", r.Burst)
	}
	return s
}

// ParseRate parses a Rate from a config string.
func ParseRate(s string) (Rate, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rate{}, fmt.Errorf("empty rate")
	}
	burst := 0
	main := s
	if i := strings.Index(s, ","); i >= 0 {
		main = strings.TrimSpace(s[:i])
		rest := strings.TrimSpace(s[i+1:])
		if !strings.HasPrefix(rest, "burst=") {
			return Rate{}, fmt.Errorf("invalid rate %q (want burst=<n>)", s)
		}
		b, err := strconv.Atoi(strings.TrimSpace(rest[len("burst="):]))
		if err != nil || b < 0 {
			return Rate{}, fmt.Errorf("invalid burst in rate %q", s)
		}
		burst = b
	}
	slash := strings.Index(main, "/")
	if slash < 0 {
		return Rate{}, fmt.Errorf("invalid rate %q (want N/s|min|h)", s)
	}
	count, err := strconv.Atoi(strings.TrimSpace(main[:slash]))
	if err != nil || count < 0 {
		return Rate{}, fmt.Errorf("invalid rate count in %q", s)
	}
	unit := strings.TrimSpace(main[slash+1:])
	var period time.Duration
	switch unit {
	case "s":
		period = time.Second
	case "min":
		period = time.Minute
	case "h":
		period = time.Hour
	default:
		return Rate{}, fmt.Errorf("invalid rate unit %q (want s, min, or h)", unit)
	}
	if burst == 0 {
		burst = count
	}
	return Rate{Count: count, Period: period, Burst: burst}, nil
}

// KeyRing is an ordered list of encryption keys. The first entry is the default
// active key when EB_KEYS_ACTIVE is unset.
type KeyRing []KeyEntry

// KeyEntry is one kid → 32-byte key mapping.
type KeyEntry struct {
	Kid string
	Key []byte // exactly 32 bytes; never logged
}

// ParseKeyRing parses `kid1:base64(32 bytes),kid2:…`.
func ParseKeyRing(s string) (KeyRing, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make(KeyRing, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		colon := strings.Index(p, ":")
		if colon <= 0 {
			return nil, fmt.Errorf("invalid key ring entry %q (want kid:base64)", maskSecret(p))
		}
		kid := strings.TrimSpace(p[:colon])
		b64 := strings.TrimSpace(p[colon+1:])
		if kid == "" {
			return nil, fmt.Errorf("empty kid in key ring")
		}
		if _, ok := seen[kid]; ok {
			return nil, fmt.Errorf("duplicate kid %q in key ring", kid)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 for kid %q", kid)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("key for kid %q must be 32 bytes, got %d", kid, len(raw))
		}
		seen[kid] = struct{}{}
		out = append(out, KeyEntry{Kid: kid, Key: raw})
	}
	return out, nil
}

// Kids returns the key IDs in order.
func (k KeyRing) Kids() []string {
	out := make([]string, len(k))
	for i, e := range k {
		out[i] = e.Kid
	}
	return out
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// ParseDuration parses Go durations plus a whole-day suffix (`7d` = 7×24h).
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") && !strings.HasSuffix(s, "ms") &&
		!strings.ContainsAny(s[:len(s)-1], "hmsuµn") {
		days, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

// ParseBool accepts only the literals "true" and "false".
func ParseBool(s string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool %q (want true or false)", s)
	}
}

// ParseStringList splits on commas, trims spaces, and drops empty items.
func ParseStringList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseCIDRList parses a comma-separated list of CIDR prefixes (netip.Prefix).
func ParseCIDRList(s string) ([]netip.Prefix, error) {
	items := ParseStringList(s)
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		p, err := netip.ParsePrefix(item)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", item)
		}
		out = append(out, p)
	}
	return out, nil
}

// isPlainLiteral reports whether s looks like a table default we can compare
// to a struct tag (digits, true/false, durations, sizes, rates, simple enums).
func isPlainLiteral(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "—" || s == "-" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "tbd") || strings.Contains(lower, "required") ||
		strings.Contains(lower, "host of") || strings.Contains(lower, "build version") ||
		strings.Contains(lower, "first kid") || strings.Contains(lower, "hostname") ||
		strings.Contains(lower, "unlimited") || strings.HasPrefix(s, "$") ||
		strings.HasPrefix(s, "EB_") || strings.Contains(lower, "ttl /") {
		return false
	}
	// Strip trailing parenthetical notes: `50000` (SP5: …)
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	// Multi-value cells like `5s`, `10m` are not a single literal for one key.
	if strings.Contains(s, "`, `") || strings.Count(s, "`") > 2 {
		return false
	}
	s = strings.Trim(s, "`")
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("./:_-+,", r) {
			continue
		}
		if r == ' ' {
			// allow "20/s, burst 40" style — not plain for our default tag
			return false
		}
		return false
	}
	return true
}
