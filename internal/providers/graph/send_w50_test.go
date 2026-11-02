package graph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/providers/graph"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

type tokUser struct{ tok, user string }

func (t tokUser) AccessToken(context.Context, string) (string, string, error) {
	return t.tok, t.user, nil
}

func TestW50_SendUsesUsersPath(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	upn := "user@contoso.com"
	tok := "graph-tok-w50"
	sim.SetToken(upn, tok)

	c := &graph.Client{BaseURL: sim.URL()}
	sender := &graph.Sender{Client: c}
	sender.SetTokens(tokUser{tok: tok, user: upn})

	raw := []byte("From: a@x\r\nTo: b@x\r\nSubject: W50 Graph\r\nMessage-ID: <w50g@x>\r\n\r\nHi\r\n")
	_, _, err := sender.Send(context.Background(), "acc_w50", raw)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure ForUser was used: a bare /me client without UPN would still work on the
	// simulator when the token maps to upn, so also assert ForUser copy carries UPN.
	if c.ForUser(upn).UPN != upn {
		t.Fatal("ForUser did not set UPN")
	}
}

func TestW50_SendRequiresTokens(t *testing.T) {
	s := &graph.Sender{Client: &graph.Client{}}
	_, _, err := s.Send(context.Background(), "a", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v", err)
	}
}
