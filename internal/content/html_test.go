package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeHTML_stripsDangerAndMapsCID(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "html", "safe.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := SanitizeHTML(string(raw), HTMLOptions{
		MapCID: func(cid string) string {
			if cid == "logo@example.com" {
				return "https://api.example/att/logo"
			}
			return ""
		},
		RemoteImages: RemoteBlock,
	})
	if strings.Contains(got.HTML, "<script") || strings.Contains(got.HTML, "onclick") ||
		strings.Contains(got.HTML, "<form") || strings.Contains(got.HTML, "<style") ||
		strings.Contains(got.HTML, "@import") {
		t.Fatalf("dangerous markup survived: %s", got.HTML)
	}
	if !strings.Contains(got.HTML, `src="https://api.example/att/logo"`) {
		t.Fatalf("cid not mapped: %s", got.HTML)
	}
	if strings.Contains(got.HTML, "tracker.example") {
		t.Fatalf("remote image not blocked: %s", got.HTML)
	}
	if !strings.Contains(got.HTML, `rel="`) || !strings.Contains(got.HTML, "noopener") ||
		!strings.Contains(got.HTML, "noreferrer") || !strings.Contains(got.HTML, `target="_blank"`) {
		t.Fatalf("link rel/target missing: %s", got.HTML)
	}
}

func TestSanitizeHTML_xssCorpus(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "html", "xss.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := SanitizeHTML(string(raw), HTMLOptions{RemoteImages: RemoteAllow})
	for _, bad := range []string{"onerror", "javascript:", "<object", "<iframe", "alert("} {
		if strings.Contains(strings.ToLower(got.HTML), bad) {
			t.Fatalf("xss residue %q in %s", bad, got.HTML)
		}
	}
	if !strings.Contains(got.HTML, "https://example.com") {
		t.Fatalf("safe link dropped: %s", got.HTML)
	}
}

func TestSanitizeHTML_remoteProxy(t *testing.T) {
	got := SanitizeHTML(`<img src="https://cdn.example/a.png">`, HTMLOptions{
		RemoteImages: RemoteProxy,
		ProxyURL:     func(u string) string { return "https://proxy.example/?u=" + u },
	})
	if !strings.Contains(got.HTML, "proxy.example") {
		t.Fatalf("proxy not applied: %s", got.HTML)
	}
}

func TestSanitizeHTML_remoteAllow(t *testing.T) {
	got := SanitizeHTML(`<img src="https://cdn.example/a.png" alt="a">`, HTMLOptions{RemoteImages: RemoteAllow})
	if !strings.Contains(got.HTML, "cdn.example") {
		t.Fatalf("allow failed: %s", got.HTML)
	}
}

func TestSanitizeHTML_truncates(t *testing.T) {
	raw := `<p>` + strings.Repeat("x", 100) + `</p>`
	got := SanitizeHTML(raw, HTMLOptions{MaxBytes: 40})
	if !got.Truncated {
		t.Fatal("expected truncated")
	}
	if len(raw) <= 40 {
		t.Fatal("fixture too small")
	}
}

func TestSanitizeHTML_empty(t *testing.T) {
	got := SanitizeHTML("", HTMLOptions{})
	if got.HTML != "" || got.Truncated {
		t.Fatalf("%+v", got)
	}
}
