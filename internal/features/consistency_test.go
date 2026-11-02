package features

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCatalogueMatchesS9(t *testing.T) {
	p := filepath.Join("..", "..", "docs", "spec", "S9-config.md")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		t.Skip("docs/spec is not part of this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	start := strings.Index(doc, "### 4.2")
	if start < 0 {
		t.Fatal("### 4.2 not found")
	}
	end := strings.Index(doc[start:], "\n## 5.")
	if end < 0 {
		end = len(doc) - start
	}
	section := doc[start : start+end]

	type row struct {
		name     string
		scopes   []Scope
		phase    string
		lockable bool
		defOn    *bool // nil = skip (enum / env-dependent)
	}
	var rows []row
	lineRe := regexp.MustCompile(`^\|\s*([^|]+)\|\s*([^|]+)\|\s*([^|]+)\|\s*([^|]+)\|\s*([^|]+)\|`)
	for _, line := range strings.Split(section, "\n") {
		if !strings.Contains(line, "`") || strings.HasPrefix(strings.TrimSpace(line), "| Switch") ||
			strings.Contains(line, "| ---") {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		nameCell := strings.TrimSpace(m[1])
		if !strings.Contains(nameCell, "`") {
			continue
		}
		// Expand comma-separated names: sinks.nats, sinks.kafka, …
		names := splitNames(nameCell)
		scopes := parseScopes(m[2])
		defCell := strings.TrimSpace(m[3])
		phase := strings.TrimSpace(m[4])
		lockCell := strings.TrimSpace(m[5])
		lockable := strings.EqualFold(lockCell, "yes")
		var defOn *bool
		switch {
		case strings.Contains(defCell, "EB_ENV"),
			strings.Contains(defCell, "follows"),
			strings.Contains(defCell, "`allow`"),
			strings.Contains(defCell, "`standard`"),
			strings.Contains(defCell, "`capture`"),
			strings.Contains(defCell, "when installed"),
			strings.Contains(defCell, "provider presets"):
			// Enum / env-dependent — skip default check (brief).
			defOn = nil
		case strings.Contains(strings.ToLower(defCell), "**on**") ||
			(strings.HasPrefix(strings.ToLower(defCell), "on") && !strings.Contains(defCell, "off")):
			v := true
			defOn = &v
		case strings.Contains(strings.ToLower(defCell), "off"):
			v := false
			defOn = &v
		default:
			defOn = nil
		}
		for _, n := range names {
			rows = append(rows, row{name: n, scopes: scopes, phase: phase, lockable: lockable, defOn: defOn})
		}
	}
	if len(rows) < 40 {
		t.Fatalf("parsed too few rows: %d", len(rows))
	}

	r := DefaultRegistry()
	for _, row := range rows {
		sw, ok := r.Get(row.name)
		if !ok {
			t.Errorf("missing switch %q", row.name)
			continue
		}
		if sw.Phase != row.phase {
			t.Errorf("%s: phase %q want %q", row.name, sw.Phase, row.phase)
		}
		if sw.Lockable != row.lockable {
			t.Errorf("%s: lockable %v want %v", row.name, sw.Lockable, row.lockable)
		}
		if !scopesEqual(sw.Scopes, row.scopes) {
			t.Errorf("%s: scopes %v want %v", row.name, sw.Scopes, row.scopes)
		}
		if row.defOn != nil && sw.Default.Bool != nil {
			if *sw.Default.Bool != *row.defOn {
				t.Errorf("%s: default %v want %v", row.name, *sw.Default.Bool, *row.defOn)
			}
		}
	}
}

func splitNames(cell string) []string {
	re := regexp.MustCompile("`([^`]+)`")
	ms := re.FindAllStringSubmatch(cell, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

func parseScopes(cell string) []Scope {
	cell = strings.TrimSpace(cell)
	var out []Scope
	for _, p := range strings.Split(cell, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "global":
			out = append(out, ScopeGlobal)
		case "organization":
			out = append(out, ScopeOrganization)
		case "account":
			out = append(out, ScopeAccount)
		case "endpoint":
			out = append(out, ScopeEndpoint)
		}
	}
	return out
}

func scopesEqual(a, b []Scope) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
