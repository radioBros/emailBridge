package gmail_test

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/mailbox"
	"github.com/radiobros/emailbridge/internal/providers/gmail"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func TestW50_OpsFetchSearchFlags(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	email := "ops@example.com"
	tok := "tok-ops"
	sim.SetToken(email, tok)
	id, _, _ := sim.PutMessage(email, []byte("From: a\r\nTo: b\r\nSubject: HelloOps\r\n\r\nbody"), []string{"INBOX", "UNREAD"})

	ops := &gmail.Ops{
		Client: &gmail.Client{BaseURL: sim.URL()}, Tokens: tokUser{tok: tok, user: email},
		AccountID: "acc_1", User: email,
	}
	raw, err := ops.FetchRaw(context.Background(), id, 1<<20)
	if err != nil || len(raw) == 0 {
		t.Fatalf("FetchRaw: %v len=%d", err, len(raw))
	}
	ids, _, err := ops.Search(context.Background(), "INBOX", mailbox.SearchQuery{Subject: "HelloOps"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range ids {
		if x == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("search missed %s in %v", id, ids)
	}
	if err := ops.SetFlags(context.Background(), id, 0, "INBOX", 0, mailbox.FlagUpdate{Add: []string{`\Seen`}}); err != nil {
		t.Fatal(err)
	}
	cats, _, err := ops.CategoriesAndLabels(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_ = cats
	if err := ops.Move(context.Background(), id, 0, "INBOX", 0, "[Gmail]/Trash"); err != nil {
		t.Fatal(err)
	}
	up, err := ops.Upload(context.Background(), mailbox.UploadInput{
		MailboxPath: "INBOX", Raw: []byte("From: a\r\nTo: b\r\nSubject: up\r\n\r\nx"),
	})
	if err != nil || up.ProviderID == "" {
		t.Fatalf("upload %v %+v", err, up)
	}
	_ = ops.Delete(context.Background(), up.ProviderID, 0, "INBOX", 0)
	_ = ops.Subscribe(context.Background(), "INBOX", true)
	_ = ops.RenameMailbox(context.Background(), "a", "b")
	_ = ops.DeleteMailbox(context.Background(), "Label/x")
	_ = ops.CreateMailbox(context.Background(), "INBOX", true)
}
