package webhook

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/pkg/webhookverify"
)

func testSecret(fill byte) string {
	return secretPrefix + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func TestSignatureHeadersStandardAndRotation(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	body := []byte(`{"ok":true}`)
	first, second := testSecret(1), testSecret(2)
	headers, err := SignatureHeaders(SignatureStandard, "evt_1", at, body, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get(webhookverify.HeaderWebhookTimestamp) != "1700000000" ||
		len(strings.Split(headers.Get(webhookverify.HeaderWebhookSignature), " ")) != 2 {
		t.Fatalf("headers = %v", headers)
	}
	if got := strings.Split(headers.Get(webhookverify.HeaderWebhookSignature), " ")[0]; got != "v1,2Sv+tG7NrJa9DqqHodF038RQJOj1BN97Hxox+eQIer8=" {
		t.Fatalf("signature vector = %s", got)
	}
	for _, secret := range []string{first, second} {
		if err := webhookverify.Verify(secret, headers, body, at, time.Minute); err != nil {
			t.Fatalf("verify %s: %v", secretPreview(secret), err)
		}
	}
}

func TestSignatureHeadersBodyOnlyAndErrors(t *testing.T) {
	secret := testSecret(3)
	headers, err := SignatureHeaders(SignatureBodyOnly, "evt_2", time.Now(), []byte("body"), []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get(webhookverify.HeaderWebhookTimestamp) != "" ||
		headers.Get(webhookverify.HeaderWebhookSignature) != "" {
		t.Fatalf("standard headers present: %v", headers)
	}
	if err := webhookverify.VerifyBodyOnly(secret, headers, []byte("body")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, id string
		secrets  []string
	}{
		{SignatureStandard, "", []string{secret}},
		{SignatureStandard, "evt", nil},
		{"bad", "evt", []string{secret}},
		{SignatureStandard, "evt", []string{"bad"}},
		{SignatureBodyOnly, "evt", []string{"whsec_bad"}},
	} {
		if _, err := SignatureHeaders(tc.mode, tc.id, time.Now(), nil, tc.secrets); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestSecretGenerationValidationAndPreview(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSecret(secret); err != nil {
		t.Fatal(err)
	}
	if secretID(secret) == "" || !strings.Contains(secretPreview(secret), "…") {
		t.Fatal("secret metadata")
	}
	if secretPreview("short") != "short" || formatUnix(0) != "0" {
		t.Fatal("short helpers")
	}
	for _, invalid := range []string{"", "x", "whsec_", "whsec_!!!!", "whsec_" + base64.StdEncoding.EncodeToString([]byte("short"))} {
		if validateSecret(invalid) == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
