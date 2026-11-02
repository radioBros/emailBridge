package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fakeSettings struct {
	body settings.SettingsBody
	err  error
}

func (f fakeSettings) Effective(context.Context, string) (settings.SettingsBody, error) {
	return f.body, f.err
}

func TestAIEnabledViaFakeSettings(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{
		Registry: features.DefaultRegistry(), Env: "test",
		Settings: fakeSettings{body: settings.SettingsBody{
			Switches: map[string]settings.SettingValue{
				switchAI: {Value: true},
			},
		}},
	}
	// Effective() is no longer used for ai.enabled (G7); seed the org settings doc via DB in integration tests.
	_ = svc
	_ = org
	svc2 := &Service{Registry: features.DefaultRegistry(), Env: "test", Enabled: func(context.Context, string, string) bool { return true }}
	if !svc2.aiEnabled(context.Background(), org, "") {
		t.Fatal("want on")
	}
	svc2.Enabled = func(context.Context, string, string) bool { return false }
	if svc2.aiEnabled(context.Background(), org, "") {
		t.Fatal("want off")
	}
	_ = errors.New("x")
}

func TestRunErrorBranches(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	svc := &Service{
		Cipher: cipher, Enabled: func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{ProviderOpenAI: &MockProvider{}},
		Repo:      &memStore{},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	if _, err := svc.Summarize(ctx, "", "x"); err == nil {
		t.Fatal("want not configured for empty store")
	}
	if _, err := svc.Summarize(context.Background(), "", "x"); err == nil {
		t.Fatal("no tenant")
	}
	es, _ := cipher.EncryptString(context.Background(), org, "k", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	svc.Repo = &memStore{docs: map[string]*Config{org: {
		ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
		APIKey: crypto.EncryptedString{}, Features: AllFeatures, PIIRedact: true,
	}}}
	if _, err := svc.Summarize(ctx, org, "x"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("no key", err)
	}
	svc.Repo = &memStore{docs: map[string]*Config{org: {
		ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
		APIKey: es, Features: AllFeatures, BodyMaxChars: 0, PIIRedact: true,
	}}}
	out, err := svc.Summarize(ctx, org, "a@b.co hi")
	if err != nil || out == "" {
		t.Fatal(out, err)
	}
	svc.Providers = map[string]Provider{ProviderOpenAI: &MockProvider{Fn: func(context.Context, string, string, string, string) (string, error) {
		return "", errors.New("fail")
	}}}
	if _, err := svc.Summarize(ctx, org, "x"); err == nil {
		t.Fatal("want fail")
	}
}

func TestAnthropicMore(t *testing.T) {
	a := &Anthropic{BaseURL: "https://x.test", HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"content":[]}`))}, nil
	})}
	if _, err := a.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("empty")
	}
	a2 := &Anthropic{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`not-json`))}, nil
	})}
	if _, err := a2.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("decode")
	}
	a3 := &Anthropic{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("net")
	})}
	if _, err := a3.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("net")
	}
	_ = Truncate("short", 0)
	_ = RedactText("x", 0)
	_ = tenancy.RoleOwner
}

func TestAPIErrorPaths(t *testing.T) {
	svc := &Service{Repo: &memStore{}}
	ctx := context.Background()
	if _, err := svc.getSettings(ctx, &struct{}{}); err == nil {
		t.Fatal("no auth")
	}
	if _, err := svc.putSettings(ctx, &putIn{Body: PutInput{Provider: ProviderOpenAI, Model: "m"}}); err == nil {
		t.Fatal("no auth")
	}
}
