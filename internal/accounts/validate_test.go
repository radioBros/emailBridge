package accounts

import (
	"encoding/json"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestValidateNetworkAndIMAPID(t *testing.T) {
	if err := validateNetwork(nil); err != nil {
		t.Fatal(err)
	}
	if err := validateNetwork(&NetworkConfig{
		Proxy: "socks5://127.0.0.1:1080", LocalAddress: "127.0.0.1",
		LocalAddresses: []string{"127.0.0.1"}, SMTPStrategy: "random", IMAPStrategy: "hash-by-account",
	}); err != nil {
		t.Fatal(err)
	}
	if errs.CodeOf(validateNetwork(&NetworkConfig{Proxy: "ftp://x"})) != errs.CodeValidationFailed {
		t.Fatal("proxy scheme")
	}
	if errs.CodeOf(validateNetwork(&NetworkConfig{LocalAddress: "not-ip"})) != errs.CodeValidationFailed {
		t.Fatal("localAddress")
	}
	if errs.CodeOf(validateNetwork(&NetworkConfig{SMTPStrategy: "least-conn"})) != errs.CodeValidationFailed {
		t.Fatal("strategy")
	}
	in := IMAPConfigInput{Host: "h", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"},
		IDName: string(make([]byte, 200))}
	if errs.CodeOf(validateIMAPInput(in, true)) != errs.CodeValidationFailed {
		t.Fatal("idName length")
	}
}

func TestValidateCreateAndPaths(t *testing.T) {
	base := CreateInput{
		Name: "A", Email: "a@example.com",
		IMAP: IMAPConfigInput{Host: "imap.example.com", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}},
		SMTP: SMTPConfigInput{Host: "smtp.example.com", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}},
	}
	if err := validateCreate(base, nil); err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.Email = "not-an-email"
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("email")
	}
	bad = base
	bad.ExternalID = "bad id!"
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("externalId")
	}
	bad = base
	bad.IMAP.Port = 0
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("port")
	}
	bad = base
	bad.IMAP.TLS = "ssl"
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("tls")
	}
	bad = base
	bad.SMTP.Policy = "once"
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("policy")
	}
	if err := validatePath("*"); err != nil {
		t.Fatal(err)
	}
	if errs.CodeOf(validatePath("/INBOX")) != errs.CodeValidationFailed {
		t.Fatal("path")
	}
	if errs.CodeOf(validateSync(&SyncConfig{ResyncDelay: "nope"})) != errs.CodeValidationFailed {
		t.Fatal("resyncDelay")
	}
	if errs.CodeOf(validateSwitches(map[string]any{"no.such": true}, nil)) != errs.CodeSwitchUnknown {
		t.Fatal("switch")
	}
	if errs.CodeOf(validateSwitches(map[string]any{"inbound.archiveRaw": "x"}, nil)) != errs.CodeValidationFailed {
		t.Fatal("switch type")
	}
	if err := validateSwitches(map[string]any{"inbound.archiveRaw": true}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLocale(t *testing.T) {
	for _, code := range []string{"", "en", "it", "es", "fr", "de", "pt", "ru", "zh", "ja"} {
		if err := validateLocale(code); err != nil {
			t.Fatalf("%q: %v", code, err)
		}
	}
	for _, tag := range []string{"it-IT", "en-US", "de-CH", "pt-PT", "zh-Hans-CN", "fr_FR", "JA", "  es-MX  "} {
		if err := validateLocale(tag); err != nil {
			t.Fatalf("region tag %q: %v", tag, err)
		}
	}
	if errs.CodeOf(validateLocale("xx")) != errs.CodeValidationFailed {
		t.Fatal("unknown locale")
	}
	if errs.CodeOf(validateLocale("xx-YY")) != errs.CodeValidationFailed {
		t.Fatal("unsupported primary")
	}
	if errs.CodeOf(validateLocale("nope")) != errs.CodeValidationFailed {
		t.Fatal("garbage locale")
	}
	ok := CreateInput{
		Name: "A", Email: "a@example.com", Locale: "it-IT",
		IMAP: IMAPConfigInput{Host: "imap.example.com", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}},
		SMTP: SMTPConfigInput{Host: "smtp.example.com", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}},
	}
	if err := validateCreate(ok, nil); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Locale = "nope"
	if errs.CodeOf(validateCreate(bad, nil)) != errs.CodeValidationFailed {
		t.Fatal("create locale")
	}
}

func TestAccountViewRedactsPasswords(t *testing.T) {
	a := Account{
		ID: "acc_01habcdefghijklmnopqrstuvw", OrganizationID: "org_01habcdefghijklmnopqrstuvw",
		Name: "N", Email: "n@e.com", State: StateNew,
		IMAP: IMAPConfig{Host: "h", Port: 993, TLS: TLSImplicit, Auth: MailAuth{User: "u", Pass: crypto.EncryptedString{DEK: "dek", CT: []byte("x")}}},
		SMTP: SMTPConfig{Host: "h", Port: 465, TLS: TLSImplicit, Auth: MailAuth{User: "u", Pass: crypto.EncryptedString{DEK: "dek", CT: []byte("y")}}},
	}
	b, err := json.Marshal(a.View())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{`"pass"`, `"ct"`, `"dek"`, `"nonce"`} {
		if containsStr(s, bad) {
			t.Fatalf("secret field leaked in JSON: %s\n%s", bad, s)
		}
	}
	if !containsStr(s, `"hasPassword":true`) {
		t.Fatalf("hasPassword missing: %s", s)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}

func TestParseETagAndSort(t *testing.T) {
	// The v-prefixed form (what every resource and the web client use) and the older bare form both parse.
	if parseETag(`"v12"`) != 12 || parseETag(`"12"`) != 12 || parseETag(`W/"v3"`) != 3 || parseETag(`"x1"`) != -1 {
		t.Fatal("etag")
	}
	f, d, err := parseSort("-email")
	if err != nil || f != "email" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, _, err := parseSort("nope"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	if formatETag(7) != `"v7"` {
		t.Fatal(formatETag(7))
	}
}
