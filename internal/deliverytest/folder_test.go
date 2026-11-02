package deliverytest

import "testing"

func TestClassifyFolder(t *testing.T) {
	cases := []struct{ path, su, want string }{
		{"INBOX", `\Inbox`, FolderInbox},
		{"Junk", `\Junk`, FolderSpam},
		{"Spam", `\Spam`, FolderSpam},
		{"INBOX.Spam", "", FolderSpam},
		{"Archive/2024", "", FolderOther},
		{"", "", FolderOther},
		{"Junk E-mail", "", FolderSpam},
		{"folder/inbox", "", FolderInbox},
		{"x.inbox", "", FolderInbox},
		{"INBOX/spam", "", FolderSpam},
	}
	for _, c := range cases {
		if got := classifyFolder(c.path, c.su); got != c.want {
			t.Errorf("classify(%q,%q)=%q want %q", c.path, c.su, got, c.want)
		}
	}
}
