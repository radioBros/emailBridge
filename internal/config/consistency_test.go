package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// computedDefaultKeys are S9 defaults that are not plain literals (hostname,
// build version, TTL/3, host of URL, $EB_DATA_DIR, first kid, EB_ENV copy, etc.).
var computedDefaultKeys = map[string]string{
	"EB_NODE_ID":             "hostname",
	"EB_CALLBACK_URL":        "copy of EB_PUBLIC_URL",
	"EB_KEYS_ACTIVE":         "first kid in the ring",
	"EB_AUTH_COOKIE_DOMAIN":  "host of EB_PUBLIC_URL",
	"EB_AUTH_WEBAUTHN_RP_ID": "host of EB_PUBLIC_URL",
	"EB_LOG_DIR":             "$EB_DATA_DIR/logs",
	"EB_SENTRY_ENVIRONMENT":  "EB_ENV",
	"EB_SENTRY_RELEASE":      "build version",
	"EB_LEASE_RENEW":         "TTL / 3",
	"EB_IMAP_ID_VERSION":     "build version",
	// Spec cell is "TBD (spike: go-imap); start value 25m" — we ship 25m.
	"EB_SYNC_MAX_IDLE": "TBD with start value 25m",
	// Rate cells written as `20/s`, burst `40` — our tag uses 20/s,burst=40.
	"EB_RATE_API_KEY": "prose burst cell",
	"EB_RATE_API_ORG": "prose burst cell",
	// `10/min` per IP — "per IP" is scope note, default tag is 10/min (checked separately).
	"EB_RATE_LOGIN": "has trailing prose in notes column only; default is plain",
}

func TestConfigKeysMatchS9(t *testing.T) {
	spec := readS9(t)
	section := extractSection(spec, "### 2.1", "### 2.14")
	specKeys := extractTableKeys(section)
	// Drop EB_TEST_* and the incidental `X_FILE` secret form of a key X mentioned in notes; real keys that end in
	// _FILE (certificate paths) stay.
	for k := range specKeys {
		_, base := specKeys[strings.TrimSuffix(k, "_FILE")]
		if strings.HasPrefix(k, "EB_TEST_") || (strings.HasSuffix(k, "_FILE") && base) {
			delete(specKeys, k)
		}
	}

	cfg := &Config{}
	metas := collectMeta(cfg)
	codeKeys := map[string]string{} // key → default tag
	for _, m := range metas {
		codeKeys[m.Key] = m.Default
	}

	var missing, extra []string
	for k := range specKeys {
		if _, ok := codeKeys[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range codeKeys {
		if _, ok := specKeys[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("key set mismatch\n missing in Config: %v\n extra in Config: %v", missing, extra)
	}

	// Defaults: compare plain literals.
	defaults := extractTableDefaults(section)
	for key, want := range defaults {
		if _, skip := computedDefaultKeys[key]; skip {
			continue
		}
		got, ok := codeKeys[key]
		if !ok {
			continue
		}
		want = normaliseDefault(want)
		got = normaliseDefault(got)
		if want == "" {
			continue
		}
		if got != want {
			t.Errorf("%s: default tag %q != S9 %q", key, got, want)
		}
	}
}

func readS9(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "docs", "spec", "S9-config.md")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		t.Skip("docs/spec is not part of this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func extractSection(doc, start, end string) string {
	i := strings.Index(doc, start)
	if i < 0 {
		return ""
	}
	j := strings.Index(doc[i+len(start):], end)
	if j < 0 {
		return doc[i:]
	}
	return doc[i : i+len(start)+j]
}

var ebKeyRe = regexp.MustCompile("`EB_[A-Z0-9_]+`")

// extractTableKeys collects EB_* keys from the Key column of markdown tables
// (first cell), ignoring notes that mention e.g. EB_KEYS_LOCAL_FILE.
func extractTableKeys(section string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "| ---") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		keyCell := cells[1]
		for _, m := range ebKeyRe.FindAllString(keyCell, -1) {
			out[strings.Trim(m, "`")] = struct{}{}
		}
	}
	return out
}

// extractTableDefaults maps key → default cell text for single-key rows with a
// plain backtick default.
func extractTableDefaults(section string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(section, "\n")
	keyDefault := regexp.MustCompile(`^\|\s*` + "`" + `(EB_[A-Z0-9_]+)` + "`" + `(?:\s*\(S\))?\s*\|\s*([^|]+)\|`)
	for _, line := range lines {
		m := keyDefault.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		def := strings.TrimSpace(m[2])
		// Strip markdown bold / backticks around the primary token.
		def = strings.ReplaceAll(def, "**", "")
		if strings.HasPrefix(def, "`") {
			if end := strings.Index(def[1:], "`"); end >= 0 {
				def = def[1 : 1+end]
			}
		}
		out[key] = strings.TrimSpace(def)
	}
	return out
}

func normaliseDefault(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`")
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, " (SP4)")
	s = strings.TrimSuffix(s, " (SP5)")
	if !isPlainLiteral("`"+s+"`") && s != "true" && s != "false" && s != "0" && s != "1.0" &&
		s != "off" && s != "all" && s != "production" && s != "embedded" && s != "local" &&
		s != "info" && s != "us-east-1" && s != "emailbridge" && s != "Default" &&
		s != "implicit" && s != "standard" && s != "EmailBridge" &&
		!regexp.MustCompile(`^[0-9]+([./][a-zA-Z0-9]+)*(,burst=[0-9]+)?$`).MatchString(s) &&
		!regexp.MustCompile(`^[0-9]+(ns|us|µs|ms|s|m|h|d)$`).MatchString(s) &&
		!regexp.MustCompile(`^[0-9]+(KiB|MiB|GiB)$`).MatchString(s) &&
		!regexp.MustCompile(`^:[0-9]+$`).MatchString(s) &&
		!strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}
