package submit

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func rawReq(raw string, env *EnvelopeInput) SubmitRequest {
	return SubmitRequest{Raw: base64.StdEncoding.EncodeToString([]byte(raw)), Envelope: env}
}

func TestBuildRawRecipients(t *testing.T) {
	const msg = "From: Me <me@example.test>\r\nTo: a@example.test, B@example.test\r\nCc: c@example.test\r\n" +
		"Bcc: hidden@example.test\r\nSubject: x\r\n\r\nbody\r\n"

	built, env, bcc, err := build(rawReq(msg, nil), &SMTPAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(env.To, ",") != "a@example.test,B@example.test" || strings.Join(env.Cc, ",") != "c@example.test" {
		t.Fatalf("visible: %+v", env)
	}
	if len(bcc) != 1 || bcc[0] != "hidden@example.test" || env.BccCount != 1 {
		t.Fatalf("bcc from header: %v %+v", bcc, env)
	}
	if strings.Contains(string(built.Raw), "hidden@example.test") {
		t.Fatal("the transmitted copy must not carry the Bcc header")
	}

	// SMTP submission: RCPT TO decides; b is not a recipient, x is blind.
	_, env, bcc, err = build(rawReq(msg, &EnvelopeInput{From: "bounce@example.test", To: []string{"a@example.test", " x@example.test ", "C@example.test"}}), &SMTPAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if env.From != "bounce@example.test" || strings.Join(env.To, ",") != "a@example.test" || strings.Join(env.Cc, ",") != "c@example.test" {
		t.Fatalf("explicit envelope visible: %+v", env)
	}
	if strings.Join(bcc, ",") != "x@example.test" {
		t.Fatalf("explicit envelope bcc: %v", bcc)
	}
	if got := env.Recipients(bcc); len(got) != 3 {
		t.Fatalf("RCPT TO: %v", got)
	}

	// Empty MAIL FROM falls back to the From header.
	_, env, _, err = build(rawReq(msg, &EnvelopeInput{To: []string{"a@example.test"}}), &SMTPAccount{})
	if err != nil || env.From != "me@example.test" {
		t.Fatalf("from fallback: %+v %v", env, err)
	}

	if _, _, _, err := build(rawReq(msg, &EnvelopeInput{To: []string{" "}}), &SMTPAccount{}); errs.CodeOf(err) != errs.CodeNoRecipients {
		t.Fatalf("empty envelope: %v", err)
	}
	if _, _, _, err := build(SubmitRequest{Envelope: &EnvelopeInput{To: []string{"a@example.test"}}, Text: "x", To: []AddressInput{{Address: "a@example.test"}}}, &SMTPAccount{Email: "me@example.test"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("envelope without raw: %v", err)
	}
	if _, _, _, err := build(SubmitRequest{Raw: "%%%"}, &SMTPAccount{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad base64: %v", err)
	}
}
