package exports

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineMailboxPathJSON(t *testing.T) {
	raw, err := json.Marshal(Line{ID: "msg_1", MailboxPath: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"mailboxPath":"INBOX"`) {
		t.Fatalf("want camelCase mailboxPath, got %s", s)
	}
	if strings.Contains(s, `"MailboxPath"`) {
		t.Fatalf("legacy tag must not appear on marshal: %s", s)
	}
	var got Line
	if err := json.Unmarshal([]byte(`{"id":"msg_1","MailboxPath":"Sent"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.MailboxPath != "Sent" {
		t.Fatalf("legacy unmarshal: %+v", got)
	}
}

func TestListJSONUsesData(t *testing.T) {
	raw, err := json.Marshal(List{Data: []Export{{ID: "exp_1"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"data"`) || strings.Contains(s, `"items"`) {
		t.Fatalf("list shape: %s", s)
	}
}
