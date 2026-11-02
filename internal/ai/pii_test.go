package ai

import (
	"strings"
	"testing"
)

func TestRedactTextHashesEmailAndTruncates(t *testing.T) {
	in := "Hello alice@example.com and bob@test.org — " + strings.Repeat("x", 100)
	out := RedactText(in, 40)
	if strings.Contains(out, "alice@") || strings.Contains(out, "bob@") {
		t.Fatalf("emails not hashed: %s", out)
	}
	if !strings.Contains(out, "addr_") {
		t.Fatalf("expected hashed addr: %s", out)
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("expected truncation: %s", out)
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("abc", 10) != "abc" {
		t.Fatal("short")
	}
	if Truncate("abcdefghij", 5) != "abcde…" {
		t.Fatalf("got %q", Truncate("abcdefghij", 5))
	}
}
