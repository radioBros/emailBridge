package graph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type failingTokens struct{}

func (failingTokens) AccessToken(context.Context, string) (string, string, error) {
	return "", "", errors.New("no token")
}

// The module hands one *Client to every account. A runner must address its own mailbox through a copy and
// never set UPN on the shared client, or concurrent accounts read each other's mailboxes.
func TestRunnersNeverMutateSharedClient(t *testing.T) {
	shared := &Client{BaseURL: "http://graph.invalid"}
	var wg sync.WaitGroup
	got := make([]string, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &Runner{Client: shared, Tokens: failingTokens{}}
			upn := []string{"a@tenant.test", "b@tenant.test"}[i%2]
			_ = r.SyncOnce(context.Background(), Account{ID: "acc", UPN: upn})
			got[i] = r.Client.userPrefix()
		}()
	}
	wg.Wait()
	if shared.UPN != "" {
		t.Fatalf("shared client UPN = %q, want unchanged", shared.UPN)
	}
	for i, p := range got {
		want := "/users/" + []string{"a@tenant.test", "b@tenant.test"}[i%2]
		if p != want {
			t.Fatalf("runner %d addresses %q, want %q", i, p, want)
		}
	}
	if (*Client)(nil).ForUser("x") != nil {
		t.Fatal("ForUser on nil should stay nil")
	}
}
