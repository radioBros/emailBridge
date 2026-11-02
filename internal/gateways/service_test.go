package gateways

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestValidateGateway(t *testing.T) {
	g := &Gateway{Name: "g", Host: "smtp.example", Port: 587, TLS: TLSStartTLS, Auth: Auth{User: "u"}}
	if err := validateSMTPGateway(g); err != nil {
		t.Fatal(err)
	}
	g.Proxy = "ftp://x"
	if err := validateSMTPGateway(g); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad proxy: %v", err)
	}
	g.Proxy = "socks5://127.0.0.1:1080"
	if err := validateSMTPGateway(g); err != nil {
		t.Fatal(err)
	}
	if err := validateCreate(CreateInput{Name: "n", Host: "h", Port: 25, TLS: TLSNone, Auth: AuthInput{User: "u"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("missing pass: %v", err)
	}
	if err := checkIfMatch(`"v2"`, 1); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if formatETag(3) != `"v3"` {
		t.Fatal(formatETag(3))
	}
	if policyOr("") != PolicyStandard || policyOr(PolicyAtMostOnce) != PolicyAtMostOnce {
		t.Fatal("policyOr")
	}
	v := ViewOf(&Gateway{ID: "gwy_1", Name: "n", Host: "h", Port: 25, TLS: TLSNone, Enabled: true}, "")
	if v.Auth == nil || v.Auth.HasPassword {
		t.Fatal("empty pass")
	}
	if v.Kind != KindSMTP {
		t.Fatalf("kind=%s", v.Kind)
	}
}

func TestValidateAPICreate(t *testing.T) {
	err := validateCreate(CreateInput{
		Name: "mg", Kind: KindMailgun, API: &APIConfig{Domain: "ex.com"},
		Credentials: &CredentialsInput{APIKey: "k"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = validateCreate(CreateInput{
		Name: "mg", Kind: KindMailgun, Host: "x",
		Credentials: &CredentialsInput{APIKey: "k"}, API: &APIConfig{Domain: "ex.com"},
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("smtp fields on api: %v", err)
	}
	err = validateCreate(CreateInput{
		Name: "s", Kind: KindSES, Credentials: &CredentialsInput{AccessKey: "a"},
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("ses needs secret: %v", err)
	}
}

func TestRedactAndHelpers(t *testing.T) {
	if redactServerText("password=secret") != "[redacted]" {
		t.Fatal("redact")
	}
	if firstNonEmpty("", "b", "c") != "b" {
		t.Fatal("first")
	}
	r := verifyErr(errs.New(errs.CodeMailConnectFailed, "nope"), time.Now())
	if r.OK || r.Code == "" {
		t.Fatalf("verifyErr: %+v", r)
	}
	if webhookURL("https://eb.example/", "gwy_1") != "https://eb.example/callbacks/gateways/gwy_1" {
		t.Fatal(webhookURL("https://eb.example/", "gwy_1"))
	}
}
