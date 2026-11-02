package mcp

import (
	"strings"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	in := `{"email":"a@b.co","body":"` + strings.Repeat("z", 100) + `"}`
	out := RedactJSON(in, 30)
	if strings.Contains(out, "a@b.co") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "addr_") {
		t.Fatal(out)
	}
	plain := RedactJSON("not-json a@b.co "+strings.Repeat("x", 50), 20)
	if strings.Contains(plain, "a@b.co") || !strings.HasSuffix(plain, "…") {
		t.Fatal(plain)
	}
}
