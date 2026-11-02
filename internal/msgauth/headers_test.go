package msgauth

import (
	"net/textproto"
	"testing"
)

func TestFromHeadersGmailLike(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results",
		`mx.google.com; dkim=pass header.d=example.com header.i=@example.com header.s=20230601 header.b=abc; spf=pass smtp.mailfrom=example.com; dmarc=pass header.from=example.com`)
	got, err := FromHeaders(h, []string{"mx.google.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceHeader || got.SPF.Result != "pass" || got.SPF.Domain != "example.com" {
		t.Fatalf("spf: %+v", got.SPF)
	}
	if len(got.DKIM) != 1 || got.DKIM[0].Result != "pass" || got.DKIM[0].Domain != "example.com" || got.DKIM[0].Selector != "20230601" {
		t.Fatalf("dkim: %+v", got.DKIM)
	}
	if got.DMARC.Result != "pass" || got.DMARC.Domain != "example.com" {
		t.Fatalf("dmarc: %+v", got.DMARC)
	}
}

func TestFromHeadersMicrosoftLike(t *testing.T) {
	h := MapHeaders{
		"Authentication-Results": {
			`spf.protection.outlook.com; spf=pass smtp.mailfrom=contoso.com; dkim=pass header.d=contoso.com header.s=selector1; dmarc=pass header.from=contoso.com; arc=pass`,
		},
	}
	got, err := FromHeaders(h, []string{"spf.protection.outlook.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SPF.Result != "pass" || got.DKIM[0].Selector != "selector1" || got.DMARC.Result != "pass" || got.ARC.Result != "pass" {
		t.Fatalf("%+v", got)
	}
}

func TestFromHeadersDovecotRspamd(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results",
		`mail.example.local; spf=softfail smtp.mailfrom=forgery@evil.example; dkim=fail header.d=evil.example header.s=dkim; dmarc=fail header.from=evil.example`)
	got, err := FromHeaders(h, []string{"mail.example.local"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SPF.Result != "softfail" || got.DKIM[0].Result != "fail" || got.DMARC.Result != "fail" {
		t.Fatalf("%+v", got)
	}
}

func TestFromHeadersUntrustedOnTopIgnored(t *testing.T) {
	h := textproto.MIMEHeader{}
	// Topmost (first) is untrusted — must be ignored; trusted below wins.
	h.Add("Authentication-Results", `evil.example; spf=pass smtp.mailfrom=evil.example; dkim=pass header.d=evil.example header.s=x`)
	h.Add("Authentication-Results", `mx.trusted.example; spf=fail smtp.mailfrom=real.example; dkim=fail header.d=real.example header.s=s1; dmarc=fail header.from=real.example`)
	got, err := FromHeaders(h, []string{"mx.trusted.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SPF.Result != "fail" || got.SPF.Domain != "real.example" {
		t.Fatalf("expected trusted header, got %+v", got)
	}
	if len(got.DKIM) != 1 || got.DKIM[0].Domain != "real.example" {
		t.Fatalf("dkim: %+v", got.DKIM)
	}
}

func TestFromHeadersTrustedCaseInsensitive(t *testing.T) {
	h := MapHeaders{"authentication-results": {`MX.Example.COM; spf=neutral smtp.helo=mail.example.com`}}
	got, err := FromHeaders(h, []string{"mx.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SPF.Result != "neutral" {
		t.Fatalf("%+v", got.SPF)
	}
}

func TestFromHeadersEmptyTrusted(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results", `mx.google.com; spf=pass smtp.mailfrom=a.com`)
	got, err := FromHeaders(h, nil)
	if err != nil || got.SPF.Result != "none" || len(got.DKIM) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestFromHeadersMultipleDKIM(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results",
		`auth.example; dkim=pass header.d=a.example header.s=s1; dkim=pass header.d=b.example header.s=s2; spf=pass smtp.mailfrom=a.example`)
	got, err := FromHeaders(h, []string{"auth.example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DKIM) != 2 || got.DKIM[0].Selector != "s1" || got.DKIM[1].Selector != "s2" {
		t.Fatalf("%+v", got.DKIM)
	}
}

func TestNormalizeResult(t *testing.T) {
	cases := map[string]string{
		"pass": "pass", "PASS": "PASS", "hardfail": "fail", "policy": "fail", "": "none",
	}
	for in, want := range cases {
		if in == "PASS" {
			// normalizeResult expects already-lowercased from authres
			continue
		}
		if got := normalizeResult(in); got != want {
			t.Fatalf("%q → %q want %q", in, got, want)
		}
	}
}
