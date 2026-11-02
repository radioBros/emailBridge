package mailbox_test

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox"
)

func TestUnsupported(t *testing.T) {
	err := mailbox.Unsupported("nope")
	if errs.CodeOf(err) != errs.CodeUnsupportedForProvider {
		t.Fatalf("code %s", errs.CodeOf(err))
	}
}

func TestSplitGMLabels(t *testing.T) {
	cats, labs := mailbox.SplitGMLabels([]string{"CATEGORY_PERSONAL", "INBOX", "MyLabel", "STARRED"})
	if len(cats) != 1 || cats[0] != "CATEGORY_PERSONAL" {
		t.Fatalf("cats=%v", cats)
	}
	if len(labs) != 1 || labs[0] != "MyLabel" {
		t.Fatalf("labs=%v", labs)
	}
}
