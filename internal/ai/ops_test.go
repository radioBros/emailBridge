package ai

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fakeMsgText struct {
	text string
	err  error
}

func (f fakeMsgText) PlainText(context.Context, string, string, int) (string, error) {
	return f.text, f.err
}

func TestMessageOps(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	es, _ := cipher.EncryptString(context.Background(), org, "k", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	svc := &Service{
		Cipher: cipher, Enabled: func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{ProviderOpenAI: &MockProvider{}},
		Repo: &memStore{docs: map[string]*Config{org: {
			ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
			APIKey: es, Features: AllFeatures, BodyMaxChars: 1000,
		}}},
		Messages: fakeMsgText{text: "Hello from the email body about invoices."},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	sum, err := svc.SummarizeMessage(ctx, "acc_1", "msg_1")
	if err != nil || sum == "" {
		t.Fatal(sum, err)
	}
	label, err := svc.ClassifyMessage(ctx, "acc_1", "msg_1", []string{"sales", "support"})
	if err != nil || label == "" {
		t.Fatal(label, err)
	}
	data, err := svc.ExtractMessage(ctx, "acc_1", "msg_1", "{amount}")
	if err != nil || data == "" {
		t.Fatal(data, err)
	}
	risk, _, err := svc.RiskMessage(ctx, "acc_1", "msg_1")
	if err != nil || risk == "" {
		t.Fatal(risk, err)
	}
	if _, err := svc.ClassifyMessage(ctx, "acc_1", "msg_1", nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	svc.Messages = fakeMsgText{err: errs.New(errs.CodeNotFound, "missing")}
	if _, err := svc.SummarizeMessage(ctx, "acc_1", "msg_1"); err == nil {
		t.Fatal("want err")
	}
}

func TestSplitRiskAndTextFromEvent(t *testing.T) {
	r, rat := splitRisk("medium\nlooks phishy")
	if r != "medium" || rat == "" {
		t.Fatal(r, rat)
	}
	if textFromEventData(map[string]any{"subject": "S"}) != "S" {
		t.Fatal()
	}
	if textFromEventData(map[string]any{"text": map[string]any{"plain": "P"}}) != "P" {
		t.Fatal()
	}
}

func TestEnrichWithMock(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	es, _ := cipher.EncryptString(context.Background(), org, "k", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	svc := &Service{
		Cipher: cipher, Enabled: func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{ProviderOpenAI: &MockProvider{}},
		Repo: &memStore{docs: map[string]*Config{org: {
			ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
			APIKey: es, Features: AllFeatures, BodyMaxChars: 1000,
		}}},
	}
	out := svc.EnrichMessageReceived(context.Background(), org, "acc", map[string]any{
		"text": map[string]any{"plain": "Please review the attached invoice."},
	})
	if out == nil || out["summary"] == nil {
		t.Fatalf("%v", out)
	}
}

func TestRiskHelper(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	es, _ := cipher.EncryptString(context.Background(), org, "k", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	svc := &Service{
		Cipher: cipher, Enabled: func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{ProviderOpenAI: &MockProvider{}},
		Repo: &memStore{docs: map[string]*Config{org: {
			ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
			APIKey: es, Features: AllFeatures,
		}}},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	out, err := svc.Risk(ctx, org, "suspicious link click here")
	if err != nil || out == "" {
		t.Fatal(out, err)
	}
}
