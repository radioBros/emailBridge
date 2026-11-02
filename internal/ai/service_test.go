package ai

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memKeys struct {
	mu   sync.Mutex
	keys map[string]crypto.DataKey
}

func (m *memKeys) Active(_ context.Context, orgID string) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.keys {
		if k.Status != crypto.DEKActive {
			continue
		}
		if orgID == "" && k.OrganizationID == nil {
			cp := k
			return &cp, nil
		}
		if k.OrganizationID != nil && *k.OrganizationID == orgID {
			cp := k
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memKeys) Get(_ context.Context, id string) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return nil, crypto.ErrNotFound
	}
	return &k, nil
}
func (m *memKeys) Insert(_ context.Context, k crypto.DataKey) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys == nil {
		m.keys = map[string]crypto.DataKey{}
	}
	m.keys[k.ID] = k
	return &k, nil
}
func (m *memKeys) All(context.Context) ([]crypto.DataKey, error) { return nil, nil }
func (m *memKeys) UpdateWrap(context.Context, string, string, []byte) error {
	return nil
}
func (m *memKeys) Retire(context.Context, string) error { return nil }

type memStore struct {
	mu   sync.Mutex
	docs map[string]*Config
}

func (r *memStore) Get(_ context.Context, orgID string) (*Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.docs[orgID]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (r *memStore) Upsert(_ context.Context, c *Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.docs == nil {
		r.docs = map[string]*Config{}
	}
	cp := *c
	r.docs[c.ID] = &cp
	return nil
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = 7
	}
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: &memKeys{keys: map[string]crypto.DataKey{}}, NewID: func() string {
		return store.NewID(store.PrefixDataKey)
	}}
}

func orgCtx(org string, role tenancy.Role) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_test", OrganizationID: org, Role: role,
	})
}

func TestValidatePutAndView(t *testing.T) {
	if err := validatePut(PutInput{Provider: "x", Model: "m"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validatePut(PutInput{Provider: ProviderLocal, Model: "m"}); err == nil {
		t.Fatal("local needs baseUrl")
	}
	if err := validatePut(PutInput{Provider: ProviderOpenAI, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	bad := 10
	if err := validatePut(PutInput{Provider: ProviderOpenAI, Model: "m", BodyMaxChars: &bad}); err == nil {
		t.Fatal("body max")
	}
	if err := validatePut(PutInput{Provider: ProviderOpenAI, Model: "m", Features: []string{"nope"}}); err == nil {
		t.Fatal("bad feature")
	}
	c := Config{Provider: ProviderOpenAI, Model: "gpt", PIIRedact: true, BodyMaxChars: 100}
	v := c.view()
	if !v.PIIRedact || len(v.Features) != len(AllFeatures) || v.APIKeySet {
		t.Fatalf("%+v", v)
	}
}

func TestPutGetSettings(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	svc := &Service{
		Repo: &memStore{}, Cipher: cipher, Registry: features.DefaultRegistry(), Env: "test",
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	key := "sk-secret"
	pii := true
	max := 4000
	v, err := svc.PutSettings(ctx, PutInput{
		Provider: ProviderOpenAI, Model: "gpt-4o-mini", APIKey: &key,
		Features: []string{FeatureSummary, FeatureClassification}, PIIRedact: &pii, BodyMaxChars: &max,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !v.APIKeySet || v.Model != "gpt-4o-mini" || len(v.Features) != 2 {
		t.Fatalf("%+v", v)
	}
	got, err := svc.GetSettings(ctx)
	if err != nil || !got.APIKeySet {
		t.Fatal(got, err)
	}
	v2, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt-4o"})
	if err != nil || !v2.APIKeySet {
		t.Fatal(v2, err)
	}
	empty := ""
	v3, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt-4o", APIKey: &empty})
	if err != nil || v3.APIKeySet {
		t.Fatal(v3, err)
	}
	emptyGet, err := svc.GetSettings(orgCtx(store.NewID(store.PrefixOrganization), tenancy.RoleOwner))
	if err != nil || emptyGet.APIKeySet || emptyGet.Provider != ProviderOpenAI {
		t.Fatal(emptyGet, err)
	}
}

func TestSummarizeClassifyExtract(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	cipher := testCipher(t)
	es, err := cipher.EncryptString(context.Background(), org, "sk", crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &memStore{docs: map[string]*Config{
		org: {
			ID: org, OrganizationID: org, Provider: ProviderOpenAI, Model: "m",
			APIKey: es, Features: AllFeatures, PIIRedact: true, BodyMaxChars: 200,
		},
	}}
	var sawUser string
	svc := &Service{
		Repo: st, Cipher: cipher, Registry: features.DefaultRegistry(), Env: "test",
		Enabled: func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{
			ProviderOpenAI: &MockProvider{Fn: func(_ context.Context, _, _, _, user string) (string, error) {
				sawUser = user
				return "summary", nil
			}},
		},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	out, err := svc.Summarize(ctx, org, "hi alice@example.com")
	if err != nil || out != "summary" {
		t.Fatal(out, err)
	}
	if strings.Contains(sawUser, "alice@") {
		t.Fatalf("PII not redacted: %s", sawUser)
	}
	svc.Providers[ProviderOpenAI] = &MockProvider{Fn: func(_ context.Context, _, _, _, _ string) (string, error) {
		return "spam", nil
	}}
	label, err := svc.Classify(ctx, org, "this is spam", []string{"ham", "spam"})
	if err != nil || label != "spam" {
		t.Fatal(label, err)
	}
	js, err := svc.Extract(ctx, org, "Name: Bob", `{"name":"string"}`)
	if err != nil || js == "" {
		t.Fatal(js, err)
	}
	st.docs[org].Features = []string{FeatureSummary}
	if _, err := svc.Classify(ctx, org, "x", []string{"a"}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
	svc.Enabled = func(context.Context, string, string) bool { return false }
	if _, err := svc.Summarize(ctx, org, "x"); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
}

func TestProviderOfAndHelpers(t *testing.T) {
	svc := &Service{}
	if _, err := svc.providerOf(&Config{Provider: ProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.providerOf(&Config{Provider: ProviderLocal, BaseURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("loopback http must be refused")
	}
	svc.Egress = egress.NewDialer(egress.Policy{AllowHosts: []string{"llm.local"}})
	if _, err := svc.providerOf(&Config{Provider: ProviderLocal, BaseURL: "http://llm.local:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.providerOf(&Config{Provider: ProviderAnthropic}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.providerOf(&Config{Provider: "nope"}); err == nil {
		t.Fatal("want err")
	}
	if matchLabel("HAM", []string{"ham", "spam"}) != "ham" {
		t.Fatal("fold")
	}
	if len(uniqueFeatures([]string{"summary", "summary", "extraction"})) != 2 {
		t.Fatal("unique")
	}
	if _, err := (&Service{}).Classify(context.Background(), "org", "x", nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := (&Service{}).Extract(context.Background(), "org", "x", ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestViewerForbidden(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &memStore{}, Cipher: testCipher(t)}
	ctx := orgCtx(org, tenancy.RoleViewer)
	key := "x"
	if _, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "m", APIKey: &key}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
}

func TestModuleMigration(t *testing.T) {
	m := &Module{Env: "test"}
	if m.Name() != "ai" || m.Service() != nil {
		t.Fatal("meta")
	}
	mig := Migration(29)
	if mig.Number != 29 || mig.Name == "" {
		t.Fatal(mig)
	}
	_ = m.Stop(context.Background())
}
