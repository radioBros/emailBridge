package graph

import "testing"

func TestMailboxPathAndFlags(t *testing.T) {
	path, su := MailboxPath("inbox", "Inbox")
	if path != "INBOX" || su != `\Inbox` {
		t.Fatalf("inbox %s %s", path, su)
	}
	path, su = MailboxPath("", "Projects")
	if path != "Projects" || su != "" {
		t.Fatalf("custom %s %q", path, su)
	}
	if PathForWellKnown("sentitems") != "Sent Items" {
		t.Fatal(PathForWellKnown("sentitems"))
	}
	if SpecialUseFor("junkemail") != `\Junk` {
		t.Fatal(SpecialUseFor("junkemail"))
	}
	flags := FlagsFromGraph(true, true, true)
	if len(flags) != 3 {
		t.Fatalf("%v", flags)
	}
	flags = FlagsFromGraph(false, false, false)
	if len(flags) != 0 {
		t.Fatalf("%v", flags)
	}
}
