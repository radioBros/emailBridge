package submit

import (
	"strings"
	"testing"
)

func TestHeaderAddressedRaw(t *testing.T) {
	raw := "From: a@example.test\r\nTo: real@example.test,\r\n  other@example.test\r\nCc: cc@example.test\r\nSubject: Hi\r\n\r\nTo: not-a-header@example.test\r\n"

	if got := string(headerAddressedRaw([]byte(raw), nil, nil)); got != raw {
		t.Fatalf("unchanged without bcc or rewrite: %q", got)
	}

	got := string(headerAddressedRaw([]byte(raw), []string{"hidden@example.test"}, []string{"test@example.test"}))
	head, body, _ := strings.Cut(got, "\r\n\r\n")
	for _, gone := range []string{"real@example.test", "other@example.test", "cc@example.test", "hidden@example.test"} {
		if strings.Contains(head, gone) {
			t.Fatalf("rewrite kept %s: %q", gone, head)
		}
	}
	if !strings.HasPrefix(head, "To: test@example.test\r\n") || !strings.Contains(head, sandboxRewriteHeader) ||
		!strings.Contains(head, "Subject: Hi") || !strings.Contains(head, "From: a@example.test") {
		t.Fatalf("rewritten head: %q", head)
	}
	if body != "To: not-a-header@example.test\r\n" {
		t.Fatalf("body must not change: %q", body)
	}

	got = string(headerAddressedRaw([]byte(raw), []string{"b1@example.test", "b2@example.test"}, nil))
	if !strings.HasPrefix(got, "Bcc: b1@example.test, b2@example.test\r\n") || !strings.Contains(got, "To: real@example.test,\r\n  other@example.test") {
		t.Fatalf("bcc: %q", got)
	}

	lf := "From: a@example.test\nBcc: stale@example.test\nTo: x@example.test\n\nbody"
	got = string(headerAddressedRaw([]byte(lf), []string{"b@example.test"}, nil))
	if strings.Contains(got, "stale@") || !strings.HasPrefix(got, "Bcc: b@example.test\n") || !strings.HasSuffix(got, "\n\nbody") {
		t.Fatalf("LF message: %q", got)
	}

	noBody := "From: a@example.test\r\nTo: x@example.test\r\n"
	if got := string(headerAddressedRaw([]byte(noBody), nil, []string{"t@example.test"})); strings.Contains(got, "x@example.test") {
		t.Fatalf("headers only: %q", got)
	}
}
