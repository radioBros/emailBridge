package accounts

import "testing"

func TestValidateProviderAndCreateGmail(t *testing.T) {
	if err := validateProvider("nope"); err == nil {
		t.Fatal("bad provider")
	}
	in := CreateInput{
		Name: "G", Email: "a@ex.com", Provider: ProviderGmail,
		SkipVerify: true,
	}
	if err := validateCreate(in, nil); err != nil {
		t.Fatal(err)
	}
	in.Provider = "imap"
	in.IMAP = IMAPConfigInput{Host: "h", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}}
	in.SMTP = SMTPConfigInput{Host: "h", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}}
	if err := validateCreate(in, nil); err != nil {
		t.Fatal(err)
	}
	if normalizeProvider("") != ProviderIMAP {
		t.Fatal("default")
	}
	if !providerAPI(ProviderGmail) || providerAPI(ProviderIMAP) {
		t.Fatal("api")
	}
	if err := ValidateGraphAccount(nil, GraphAuth{}); err != nil {
		t.Fatal(err)
	}
	ga := &Account{Provider: ProviderGraph}
	if err := ValidateGraphAccount(ga, GraphAuth{AppOnly: true}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGraphAccount(ga, GraphAuth{}); err == nil {
		t.Fatal("want oauth required")
	}
	ga.OAuth = &OAuthLink{AppID: "app1"}
	if err := ValidateGraphAccount(ga, GraphAuth{OAuthAppProvider: "google"}); err == nil {
		t.Fatal("want outlook app")
	}
	if err := ValidateGraphAccount(ga, GraphAuth{OAuthAppProvider: OutlookOAuthProvider}); err != nil {
		t.Fatal(err)
	}
}
