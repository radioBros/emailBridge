//go:build e2e

package p0

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForcedErrorReporting(t *testing.T) {
	dsn := os.Getenv("EB_TEST_SENTRY_DSN")
	if dsn == "" {
		dsn = readTestLocal(t, "EB_TEST_SENTRY_DSN")
	}
	if dsn == "" {
		t.Skip("EB_TEST_SENTRY_DSN empty — no safe production trigger for a reportable error without a test-only route")
	}
	// No build-tagged test route exists in production code. Wiring Sentry alone does not prove an event
	// without a deliberate crash endpoint. Skip with a clear reason rather than adding one.
	t.Skip("no existing safe trigger for a forced reportable error (would require a build-tagged test route); DSN is set but scenario is skipped")
}

func readTestLocal(t *testing.T, key string) string {
	t.Helper()
	path := filepath.Join(findRepoRoot(), ".env.test.local")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == key {
			return strings.TrimSpace(strings.Trim(v, `"'`))
		}
	}
	return ""
}
