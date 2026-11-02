package connect

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type startCall struct{ org, email, appID, linkID, accountID, returnURL string }

func oauthStartService(t *testing.T, st *memStore, calls *[]startCall) *Service {
	t.Helper()
	s := NewService(st, nil)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	s.OAuthStart = func(_ context.Context, org, email, appID, linkID, accountID, returnURL string) (string, error) {
		*calls = append(*calls, startCall{org, email, appID, linkID, accountID, returnURL})
		return "https://idp.example/auth", nil
	}
	return s
}

func TestOAuthStartPublicPassesLinkAccountAndReturnURL(t *testing.T) {
	st := newMemStore()
	var calls []startCall
	s := oauthStartService(t, st, &calls)
	linkApp := store.NewID(store.PrefixOAuthApp)
	otherApp := store.NewID(store.PrefixOAuthApp)

	// oauth-mode link: bound to its account and app, returnUrl carried through.
	token, link := putLink(st, "https://gw.example.com/done")
	link.Mode = ""
	link.AccountID = "acc_bound"
	link.AppID = linkApp
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil || boot.Mode != ModeOAuth {
		t.Fatalf("%+v %v", boot, err)
	}
	if _, err := s.OAuthStartPublic(context.Background(), token, OAuthStartInput{
		Email: "user@gmail.com", AppID: otherApp, CSRF: boot.CSRF,
	}); err != nil {
		t.Fatal(err)
	}
	want := startCall{"org_1", "user@gmail.com", linkApp, link.ID, "acc_bound", "https://gw.example.com/done"}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}

	// hosted link: no account (the wiring creates one per link), app from the page.
	token2, link2 := putLink(st, "")
	link2.AccountID = "acc_must_not_be_used"
	boot2, err := s.Bootstrap(context.Background(), token2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthStartPublic(context.Background(), token2, OAuthStartInput{
		Email: "user@gmail.com", AppID: otherApp, CSRF: boot2.CSRF,
	}); err != nil {
		t.Fatal(err)
	}
	if got := calls[1]; got.accountID != "" || got.appID != otherApp || got.linkID != link2.ID {
		t.Fatalf("hosted call = %+v", got)
	}

	// The hosted page switch also gates the OAuth start of a hosted link.
	s.Enabled = func(context.Context, string) bool { return false }
	if _, err := s.OAuthStartPublic(context.Background(), token2, OAuthStartInput{
		Email: "user@gmail.com", AppID: otherApp, CSRF: boot2.CSRF,
	}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("switch off: %v", err)
	}
	if len(calls) != 2 {
		t.Fatal("no start when disabled")
	}
}
