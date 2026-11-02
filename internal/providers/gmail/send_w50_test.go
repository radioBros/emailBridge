package gmail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/providers/gmail"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

type tokUser struct{ tok, user string }

func (t tokUser) AccessToken(context.Context, string) (string, string, error) {
	return t.tok, t.user, nil
}

func TestW50_SendUsesUsersPath(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	email := "sa-user@example.com"
	tok := "gmail-tok-w50"
	sim.SetToken(email, tok)

	c := &gmail.Client{BaseURL: sim.URL()}
	sender := &gmail.Sender{Client: c}
	sender.SetTokens(tokUser{tok: tok, user: email})

	raw := []byte("From: a@x\r\nTo: b@x\r\nSubject: W50\r\nMessage-ID: <w50@x>\r\n\r\nHi\r\n")
	id, thr, err := sender.Send(context.Background(), "acc_w50", raw)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || thr == "" {
		t.Fatalf("empty ids id=%q thr=%q", id, thr)
	}
	// Message landed in the user's mailbox (users/{email} path).
	got, err := c.ForUser(email).GetMessage(context.Background(), tok, id, "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Fatalf("got %s want %s", got.ID, id)
	}
}

func TestW50_SendRequiresTokens(t *testing.T) {
	s := &gmail.Sender{Client: &gmail.Client{}}
	_, _, err := s.Send(context.Background(), "a", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v", err)
	}
}
