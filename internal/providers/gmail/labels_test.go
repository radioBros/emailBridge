package gmail

import "testing"

func TestPrimaryMailboxAndFlags(t *testing.T) {
	path, su, id := PrimaryMailbox([]string{"UNREAD", "INBOX", "Label_1"}, []string{"*"})
	if path != "INBOX" || su != `\Inbox` || id != "INBOX" {
		t.Fatalf("got %s %s %s", path, su, id)
	}
	path, _, _ = PrimaryMailbox([]string{"SENT"}, []string{"INBOX"})
	if path != "" {
		t.Fatalf("unwatched SENT should be empty, got %s", path)
	}
	flags := FlagsFromLabels([]string{"UNREAD", "STARRED"})
	if len(flags) != 1 || flags[0] != `\Flagged` {
		t.Fatalf("flags %v", flags)
	}
	flags = FlagsFromLabels([]string{"STARRED"})
	if len(flags) != 2 {
		t.Fatalf("seen+flagged %v", flags)
	}
	labs := UserLabels([]string{"INBOX", "UNREAD", "Label_1", "CATEGORY_PERSONAL"}, map[string]string{"Label_1": "Work"})
	if len(labs) != 1 || labs[0] != "Work" {
		t.Fatalf("labels %v", labs)
	}
}

func TestPathForLabel(t *testing.T) {
	if PathForLabel("INBOX") != "INBOX" || LabelForPath("INBOX") != "INBOX" {
		t.Fatal("inbox map")
	}
	if PathForLabel("NOPE") != "" {
		t.Fatal("unknown")
	}
}
