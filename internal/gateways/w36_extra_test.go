package gateways

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestValidateAPIConfigAndEgress(t *testing.T) {
	if err := validateAPIConfig(KindMailgun, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateAPIConfig(KindSES, &APIConfig{BaseURL: "not-a-url"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateAPIConfig(KindSES, &APIConfig{BaseURL: "https://api.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := checkBaseURLEgress(context.Background(), nil, "https://x"); err != nil {
		t.Fatal(err)
	}
	d := egress.NewDialer(egress.Policy{BlockPrivate: true})
	if err := checkBaseURLEgress(context.Background(), d, "http://127.0.0.1/"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("private base: %v", err)
	}
	if err := checkBaseURLEgress(context.Background(), d, "://bad"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateAPICredsRequired(KindPostmark, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateCreate(CreateInput{Name: "x", Kind: "nope"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := refuseAPIFieldsOnSMTP(PatchInput{}); err != nil {
		t.Fatal(err)
	}
	if webhookURL("", "gwy_1") != "" {
		t.Fatal("empty base")
	}
}

func TestClassifySendErrBranches(t *testing.T) {
	cases := []struct {
		in   error
		code errs.Code
	}{
		{errors.New("x kind=auth code=smtp_auth_failed"), errs.CodeSMTPAuthFailed},
		{errors.New("kind=rate_limited code=smtp_rate_limited"), errs.CodeSMTPRateLimited},
		{errors.New("kind=permanent code=smtp_permanent"), errs.CodeSMTPPermanent},
		{errors.New("kind=temporary code=smtp_temporary"), errs.CodeSMTPTemporary},
		{errors.New("other"), errs.CodeSMTPTemporary},
		{errs.New(errs.CodeSMTPAuthFailed, "already"), errs.CodeSMTPAuthFailed},
	}
	for _, c := range cases {
		got := classifySendErr(c.in)
		if errs.CodeOf(got) != c.code {
			t.Fatalf("%v → %s want %s", c.in, errs.CodeOf(got), c.code)
		}
	}
	if classifySendErr(nil) != nil {
		t.Fatal("nil")
	}
}

func TestSendNilAndUnknown(t *testing.T) {
	svc := &Service{}
	if _, err := svc.Send(context.Background(), nil, OutboundMail{}); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatal(err)
	}
	if _, err := svc.Send(context.Background(), &ResolvedAPI{Kind: "nope"}, OutboundMail{}); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatal(err)
	}
}

func TestSNSStringToSignConfirm(t *testing.T) {
	s := snsStringToSign(snsEnvelope{
		Type: "SubscriptionConfirmation", Message: "m", MessageId: "1",
		SubscribeURL: "https://x", Timestamp: "t", Token: "tok", TopicArn: "arn",
	})
	if !strings.Contains(s, "SubscribeURL") || !strings.Contains(s, "Token") {
		t.Fatal(s)
	}
	_, _, err := unwrapSNS([]byte(`{`))
	if err == nil {
		t.Fatal("bad json")
	}
	ok, _, _ := verifySES(nil, []byte(`{}`), time.Now())
	if ok {
		t.Fatal("empty")
	}
	ok, _, _ = verifySES(nil, []byte(`{"Type":"Other","SigningCertURL":"https://sns.us-east-1.amazonaws.com/c","Signature":"x"}`), time.Now())
	if ok {
		t.Fatal("other type")
	}
}

func TestWebhookLimiterAndBearer(t *testing.T) {
	l := newCallbackLimiter(0)
	now := time.Now()
	for i := 0; i < 60; i++ {
		if err := l.take("k", now); err != nil {
			t.Fatalf("i=%d %v", i, err)
		}
	}
	if err := l.take("k", now); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatal(err)
	}
	h := http.Header{}
	if bearerToken(h) != "" {
		t.Fatal("empty")
	}
	h.Set("Authorization", "Basic x")
	if bearerToken(h) != "" {
		t.Fatal("basic")
	}
}

func TestHandleDeliveryWebhookRejects(t *testing.T) {
	svc := &Service{limiter: newCallbackLimiter(60)}
	req := httptest.NewRequest(http.MethodPost, "/callbacks/gateways/gwy_bad", strings.NewReader("{}"))
	if err := svc.HandleDeliveryWebhook(context.Background(), "gwy_bad", req); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestSecureEqualEmpty(t *testing.T) {
	if secureEqual("", "x") || secureEqual("x", "") {
		t.Fatal("empty")
	}
	ok, _, _ := verifyProviderSignature("unknown", "s", nil, "", nil, time.Now())
	if ok {
		t.Fatal("unknown")
	}
}

func TestServiceAuthGuards(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateInput{Name: "n"}); err == nil {
		t.Fatal("create needs tenancy")
	}
	if _, err := svc.Get(ctx, "gwy_x"); err == nil {
		t.Fatal("get needs tenancy")
	}
	if _, err := svc.List(ctx, ListFilter{}); err == nil {
		t.Fatal("list needs tenancy")
	}
	if _, err := svc.Patch(ctx, "gwy_x", "", PatchInput{}); err == nil {
		t.Fatal("patch needs tenancy")
	}
	if err := svc.Delete(ctx, "gwy_x", ""); err == nil {
		t.Fatal("delete needs tenancy")
	}
	if _, err := svc.Verify(ctx, "gwy_x"); err == nil {
		t.Fatal("verify needs tenancy")
	}
	svc.audit(ctx, "x", "y", nil) // nil Audit
}

func TestValidateMoreBranches(t *testing.T) {
	if err := validateSMTPCreate(CreateInput{
		Name: "n", Host: "h", Port: 25, TLS: TLSNone, Auth: AuthInput{User: "u", Pass: "p"},
		API: &APIConfig{},
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateAPICreate(KindMailgun, CreateInput{
		Name: strings.Repeat("n", 201), API: &APIConfig{Domain: "d"}, Credentials: &CredentialsInput{APIKey: "k"},
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validatePolicyFields("weird", nil, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateAPICredsRequired(KindSES, &CredentialsInput{AccessKey: "a", SecretKey: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := refuseAPIFieldsOnSMTP(PatchInput{API: &APIConfig{}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Brevo-Signature", "sec")
	ok, _, _ := verifyProviderSignature(KindBrevo, "sec", h, "", nil, time.Now())
	if !ok {
		t.Fatal("brevo header")
	}
}
