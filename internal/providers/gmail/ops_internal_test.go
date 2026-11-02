package gmail

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/mailbox"
)

func TestBuildGmailQuery(t *testing.T) {
	seen, unseen, flagged := true, true, true
	larger, smaller := int64(10), int64(100)
	q := mailbox.SearchQuery{
		Text: "t", Subject: "s", From: "f", To: "to",
		Seen: &seen, Unseen: &unseen, Flagged: &flagged,
		Larger: &larger, Smaller: &smaller, Labels: []string{"L"},
		HeaderName: "Message-ID", HeaderValue: "<x>",
	}
	got := buildGmailQuery(q)
	if got == "" || itoa(42) != "42" || itoa(-1) != "0" {
		t.Fatalf("q=%q itoa=%s", got, itoa(42))
	}
	add, rem := flagsToLabels(mailbox.FlagUpdate{Add: []string{`\Seen`, `\Flagged`}, Remove: []string{`\Draft`}})
	if len(add)+len(rem) == 0 {
		t.Fatal("flags")
	}
	add, rem = flagsToLabels(mailbox.FlagUpdate{Set: []string{`\Seen`, `\Flagged`}})
	if len(add)+len(rem) == 0 {
		t.Fatal("set flags")
	}
}
