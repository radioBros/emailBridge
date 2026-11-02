package graph_test

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/mailbox"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func TestW50_GraphOpsFetchMoveFlags(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	upn := "ops@contoso.com"
	tok := "tok-ops-g"
	sim.SetToken(upn, tok)
	raw := []byte("From: a@x\r\nTo: b@x\r\nSubject: GraphOps\r\nMessage-ID: <gop@x>\r\n\r\nHi\r\n")
	id, _, _ := sim.PutMessage(upn, raw, "inbox")

	ops := &graph.Ops{
		Client: &graph.Client{BaseURL: sim.URL()}, Tokens: tokUser{tok: tok, user: upn},
		AccountID: "acc_1", UPN: upn,
	}
	got, err := ops.FetchRaw(context.Background(), id, 1<<20)
	if err != nil || len(got) == 0 {
		t.Fatalf("FetchRaw: %v", err)
	}
	if err := ops.SetFlags(context.Background(), id, 0, "INBOX", 0, mailbox.FlagUpdate{Add: []string{`\Seen`, `\Flagged`}}); err != nil {
		t.Fatal(err)
	}
	ids, _, err := ops.Search(context.Background(), "INBOX", mailbox.SearchQuery{Text: "GraphOps", UseOutlookSearch: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = ids
	if err := ops.Move(context.Background(), id, 0, "INBOX", 0, "Deleted Items"); err != nil {
		t.Log(err)
	}
	up, err := ops.Upload(context.Background(), mailbox.UploadInput{
		MailboxPath: "INBOX", Raw: raw,
	})
	if err != nil {
		t.Log("upload", err)
	} else if up != nil && up.ProviderID != "" {
		_ = ops.Delete(context.Background(), up.ProviderID, 0, "INBOX", 0)
	}
	_ = ops.CreateMailbox(context.Background(), "W50Folder", false)
	_ = ops.RenameMailbox(context.Background(), "W50Folder", "W50Folder2")
	_ = ops.DeleteMailbox(context.Background(), "W50Folder2")
	_, _, _ = ops.CategoriesAndLabels(context.Background(), id)
}

func TestW50_GraphOpsUnsupported(t *testing.T) {
	ops := &graph.Ops{Client: &graph.Client{}, Tokens: tokUser{tok: "t", user: "u"}}
	if _, _, err := ops.Search(context.Background(), "", mailbox.SearchQuery{GmailRaw: "x"}); err == nil {
		t.Fatal("expected unsupported")
	}
	if err := ops.Subscribe(context.Background(), "INBOX", true); err == nil {
		t.Fatal("expected unsupported")
	}
}
