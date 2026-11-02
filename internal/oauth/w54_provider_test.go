package oauth

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestProvidersCompatible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		acc, app string
		ok       bool
	}{
		{accounts.ProviderGmail, ProviderGmail, true},
		{accounts.ProviderGmail, ProviderOutlook, false},
		{accounts.ProviderGraph, ProviderOutlook, true},
		{accounts.ProviderGraph, ProviderGmail, false},
		{accounts.ProviderIMAP, ProviderGmail, true},
		{accounts.ProviderIMAP, ProviderOutlook, true},
		{accounts.ProviderIMAP, ProviderMailru, true},
		{accounts.ProviderIMAP, ProviderGeneric, true},
		{"", ProviderGmail, true},
		{"other", ProviderGmail, false},
	}
	for _, tc := range cases {
		if got := providersCompatible(tc.acc, tc.app); got != tc.ok {
			t.Fatalf("%s+%s: got %v want %v", tc.acc, tc.app, got, tc.ok)
		}
	}
	err := providerMismatch(accounts.ProviderGraph, ProviderGmail)
	if errs.CodeOf(err) != errs.CodeOauthProviderMismatch {
		t.Fatalf("code %v", errs.CodeOf(err))
	}
}

func TestViewOfAccountOAuthEmpty(t *testing.T) {
	t.Parallel()
	v := ViewOfAccountOAuth(nil)
	if v.Connected {
		t.Fatal("expected disconnected")
	}
}
