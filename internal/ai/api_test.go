package ai

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestAPIHandlersAndRegister(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{
		Repo: &memStore{}, Cipher: testCipher(t), Registry: features.DefaultRegistry(), Env: "test",
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	out, err := svc.getSettings(ctx, &struct{}{})
	if err != nil || out.Body.Provider == "" {
		t.Fatal(out, err)
	}
	key := "sk"
	svc.Egress = egress.NewDialer(egress.Policy{
		BlockPrivate: true, BlockLinkLocal: true, AllowHosts: []string{"llm.local"},
	})
	put, err := svc.putSettings(ctx, &putIn{Body: PutInput{
		Provider: ProviderLocal, BaseURL: "http://llm.local:11434", Model: "local", APIKey: &key,
	}})
	if err != nil || !put.Body.APIKeySet {
		t.Fatal(put, err)
	}
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(srv)
}

func TestAIEnabledPaths(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Registry: features.DefaultRegistry(), Env: "production", Repo: &memStore{}}
	if svc.aiEnabled(context.Background(), org, "") {
		t.Fatal("default off")
	}
	if svc.registry() == nil {
		t.Fatal("registry")
	}
	svc.Registry = nil
	if svc.registry() == nil {
		t.Fatal("default registry")
	}
	svc.Enabled = func(context.Context, string, string) bool { return true }
	svc.Cipher = testCipher(t)
	ctx := orgCtx(org, tenancy.RoleOwner)
	if _, err := svc.Summarize(ctx, org, "x"); err == nil {
		t.Fatal("want not configured")
	}
	es, err := svc.Cipher.EncryptString(context.Background(), org, "k", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Repo = &memStore{docs: map[string]*Config{org: {
		ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
		APIKey: es, Features: AllFeatures, PIIRedact: false, BodyMaxChars: 10,
	}}}
	svc.Providers = map[string]Provider{ProviderOpenAI: &MockProvider{}}
	out, err := svc.Summarize(ctx, org, "0123456789ABCDEF")
	if err != nil || out == "" {
		t.Fatal(out, err)
	}
	_ = tenancy.RoleOwner
}

func TestModuleRoles(t *testing.T) {
	m := &Module{}
	if m.Roles() != nil {
		t.Fatal(m.Roles())
	}
}
