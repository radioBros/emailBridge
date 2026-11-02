package oauth

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestResolveLinkMode_matrix(t *testing.T) {
	gmailApp := &App{Provider: ProviderGmail, ServiceKey: crypto.EncryptedString{DEK: "d", CT: []byte{1}}}
	gmailNoKey := &App{Provider: ProviderGmail}
	outlookAO := &App{Provider: ProviderOutlook, AppOnly: true}
	outlookDeleg := &App{Provider: ProviderOutlook, AppOnly: false}

	cases := []struct {
		accProv string
		app     *App
		want    string
		code    errs.Code
	}{
		{accounts.ProviderGmail, gmailApp, AuthModeServiceAccount, ""},
		{accounts.ProviderGmail, gmailNoKey, "", errs.CodeOauthAppLinkInvalid},
		{accounts.ProviderGmail, outlookAO, "", errs.CodeOauthAppLinkInvalid},
		{accounts.ProviderGraph, outlookAO, AuthModeAppOnly, ""},
		{accounts.ProviderGraph, outlookDeleg, "", errs.CodeOauthAppLinkInvalid},
		{accounts.ProviderGraph, gmailApp, "", errs.CodeOauthAppLinkInvalid},
		{accounts.ProviderIMAP, gmailApp, "", errs.CodeOauthAppLinkInvalid},
		{"", gmailApp, "", errs.CodeOauthAppLinkInvalid},
	}
	for _, tc := range cases {
		mode, err := resolveLinkMode(tc.accProv, tc.app)
		if tc.code == "" {
			if err != nil || mode != tc.want {
				t.Fatalf("prov=%s: mode=%q err=%v want %q", tc.accProv, mode, err, tc.want)
			}
			continue
		}
		if errs.CodeOf(err) != tc.code {
			t.Fatalf("prov=%s: got %v want %s", tc.accProv, err, tc.code)
		}
	}
}

func TestViewOfAccountOAuth(t *testing.T) {
	if v := ViewOfAccountOAuth(nil); v.Connected {
		t.Fatal("nil")
	}
	v := ViewOfAccountOAuth(&AccountOAuth{AppID: "oap_x", Mode: AuthModeServiceAccount})
	if !v.Connected || v.Mode != AuthModeServiceAccount || v.AppID != "oap_x" {
		t.Fatalf("%+v", v)
	}
}
