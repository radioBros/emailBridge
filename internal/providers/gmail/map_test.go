package gmail

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestMapMessage_parityFields(t *testing.T) {
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization), Email: "a@ex.com"}
	st := newState(acc)
	raw := []byte("From: Alice <alice@ex.com>\r\nTo: Bob <bob@ex.com>\r\nCc: c@ex.com\r\nSubject: Hello\r\n" +
		"Message-ID: <abc@ex.com>\r\nDate: Mon, 26 Oct 2026 12:00:00 +0000\r\n\r\nHi there\r\n")
	msg := &Message{
		ID: "gmailMsg1", ThreadID: "gmailThr1", LabelIDs: []string{"INBOX", "UNREAD"},
		InternalDate: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), Size: int64(len(raw)), Raw: raw,
	}
	d, err := MapMessage(acc, st, "INBOX", `\Inbox`, msg, Needs{Text: true, TextMax: 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.EmailID != "gmailMsg1" || d.ThreadID != "gmailThr1" {
		t.Fatalf("provider ids %s %s", d.EmailID, d.ThreadID)
	}
	if d.Subject != "Hello" || d.From == nil || d.From.Address != "alice@ex.com" {
		t.Fatalf("envelope %+v %+v", d.Subject, d.From)
	}
	if len(d.To) != 1 || d.To[0].Address != "bob@ex.com" {
		t.Fatalf("to %v", d.To)
	}
	if d.MessageIDHeader != "<abc@ex.com>" {
		t.Fatalf("mid %q", d.MessageIDHeader)
	}
	if len(d.Flags) != 0 { // UNREAD → no \Seen
		t.Fatalf("flags %v", d.Flags)
	}
	if d.Text == nil || d.Text.Plain == "" {
		t.Fatalf("text %+v", d.Text)
	}
	if d.Labels == nil {
		t.Fatal("labels nil")
	}
}

func TestFlagDiff(t *testing.T) {
	d := FlagDiff([]string{`\Seen`}, []string{`\Seen`, `\Flagged`})
	if len(d.Added) != 1 || d.Added[0] != `\Flagged` || len(d.Removed) != 0 {
		t.Fatalf("%+v", d)
	}
}
