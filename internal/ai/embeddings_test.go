package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestEmbedTargetRules(t *testing.T) {
	key := crypto.EncryptedString{KID: "k", Nonce: []byte{1}, CT: []byte{2}}
	own := crypto.EncryptedString{KID: "k2", Nonce: []byte{3}, CT: []byte{4}}
	cases := []struct {
		name               string
		cfg                *Config
		base, model, field string
		problem            bool
	}{
		{name: "no config", cfg: nil, problem: true},
		{name: "openai reuses the main key and default model", cfg: &Config{Provider: ProviderOpenAI, APIKey: key},
			base: defaultOpenAIURL, model: DefaultEmbeddingModel, field: "apiKey"},
		{name: "openai without a key", cfg: &Config{Provider: ProviderOpenAI}, problem: true},
		{name: "openai with an own key and model", cfg: &Config{Provider: ProviderOpenAI, BaseURL: "https://gw.example/v1", APIKey: key,
			Embeddings: &EmbeddingsConfig{Model: "text-embedding-3-large", APIKey: own}},
			base: "https://gw.example/v1", model: "text-embedding-3-large", field: aadEmbeddingsKey},
		{name: "local needs a model", cfg: &Config{Provider: ProviderLocal, BaseURL: "http://ollama.internal:11434/v1"}, problem: true},
		{name: "local with a model, no key", cfg: &Config{Provider: ProviderLocal, BaseURL: "http://ollama.internal:11434/v1",
			Embeddings: &EmbeddingsConfig{Model: "nomic-embed-text"}}, base: "http://ollama.internal:11434/v1", model: "nomic-embed-text"},
		{name: "anthropic needs the block", cfg: &Config{Provider: ProviderAnthropic, APIKey: key}, problem: true},
		{name: "anthropic with an own endpoint", cfg: &Config{Provider: ProviderAnthropic, APIKey: key,
			Embeddings: &EmbeddingsConfig{BaseURL: "https://api.openai.com/v1", Model: "text-embedding-3-small", APIKey: own}},
			base: "https://api.openai.com/v1", model: "text-embedding-3-small", field: aadEmbeddingsKey},
		{name: "own base url needs a model", cfg: &Config{Provider: ProviderAnthropic,
			Embeddings: &EmbeddingsConfig{BaseURL: "https://api.openai.com/v1"}}, problem: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := c.cfg.embedTarget()
			if c.problem {
				if tg.Problem == "" {
					t.Fatalf("want a problem, got %+v", tg)
				}
				if c.cfg != nil && embeddingsFingerprint(c.cfg) != "" {
					t.Fatal("an unusable endpoint has no fingerprint")
				}
				return
			}
			if tg.Problem != "" || tg.BaseURL != c.base || tg.Model != c.model || tg.KeyField != c.field {
				t.Fatalf("got %+v", tg)
			}
			if embeddingsFingerprint(c.cfg) != c.model {
				t.Fatalf("fingerprint %q", embeddingsFingerprint(c.cfg))
			}
			v := c.cfg.embeddingsView()
			if !v.Configured || v.EffectiveModel != c.model || v.Problem != "" {
				t.Fatalf("view %+v", v)
			}
		})
	}
}

func TestValidateEmbeddingsPut(t *testing.T) {
	ok := []PutInput{
		{Provider: ProviderOpenAI, Model: "m"},
		{Provider: ProviderOpenAI, Model: "m", Embeddings: &EmbeddingsPut{}},
		{Provider: ProviderOpenAI, Model: "m", Embeddings: &EmbeddingsPut{Model: "text-embedding-3-large"}},
		{Provider: ProviderAnthropic, Model: "m"}, // embeddings simply not configured
		{Provider: ProviderAnthropic, Model: "m", Embeddings: &EmbeddingsPut{BaseURL: "https://api.openai.com/v1", Model: "e"}},
	}
	for i, in := range ok {
		if err := validatePut(in); err != nil {
			t.Fatalf("ok[%d]: %v", i, err)
		}
	}
	bad := []PutInput{
		{Provider: ProviderAnthropic, Model: "m", Embeddings: &EmbeddingsPut{Model: "e"}},
		{Provider: ProviderOpenAI, Model: "m", Embeddings: &EmbeddingsPut{BaseURL: "https://x.example/v1"}},
		{Provider: ProviderOpenAI, Model: "m", Embeddings: &EmbeddingsPut{Model: strings.Repeat("m", 201)}},
	}
	for i, in := range bad {
		if err := validatePut(in); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("bad[%d]: %v", i, err)
		}
	}
}

func TestPutSettingsEmbeddingsBlock(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	repo := &memStore{}
	var mu sync.Mutex
	changed := 0
	svc := &Service{Repo: repo, Cipher: testCipher(t), Registry: features.DefaultRegistry(), Env: "test",
		EmbeddingsChanged: func(context.Context, string) { mu.Lock(); changed++; mu.Unlock() }}
	ctx := orgCtx(org, tenancy.RoleOwner)
	key := "sk-main"
	v, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt", APIKey: &key})
	if err != nil || !v.Embeddings.Configured || v.Embeddings.EffectiveModel != DefaultEmbeddingModel || v.Embeddings.APIKeySet {
		t.Fatalf("%+v %v", v, err)
	}
	if changed != 0 {
		t.Fatal("first configuration is not a model change")
	}
	own := "sk-emb"
	v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt",
		Embeddings: &EmbeddingsPut{Model: "text-embedding-3-large", APIKey: &own}})
	if err != nil || !v.Embeddings.APIKeySet || v.Embeddings.EffectiveModel != "text-embedding-3-large" {
		t.Fatalf("%+v %v", v, err)
	}
	if changed != 1 {
		t.Fatalf("model change hook calls = %d", changed)
	}
	cfg, _ := repo.Get(ctx, org)
	if cfg.Embeddings == nil || cfg.Embeddings.APIKey.IsZero() || string(cfg.Embeddings.APIKey.CT) == own {
		t.Fatal("the embeddings key must be stored encrypted")
	}
	// The key never leaves: not in the view JSON.
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), own) || strings.Contains(string(raw), "ciphertext") {
		t.Fatalf("secret in view: %s", raw)
	}
	// Omitting the block keeps it; the same model is no change.
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt-2"}); err != nil || !v.Embeddings.APIKeySet {
		t.Fatalf("%+v %v", v, err)
	}
	if changed != 1 {
		t.Fatal("no model change")
	}
	// Removing the key keeps the model; switching to anthropic without a base URL makes it unusable (purge).
	empty := ""
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt",
		Embeddings: &EmbeddingsPut{Model: "text-embedding-3-large", APIKey: &empty}}); err != nil || v.Embeddings.APIKeySet {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderAnthropic, Model: "claude", Embeddings: &EmbeddingsPut{}}); err != nil ||
		v.Embeddings.Configured || v.Embeddings.Problem == "" {
		t.Fatalf("%+v %v", v, err)
	}
	if changed != 2 {
		t.Fatalf("unusable endpoint must trigger the purge hook, calls = %d", changed)
	}
	if cfg, _ = repo.Get(ctx, org); cfg.Embeddings != nil {
		t.Fatal("an empty block is removed")
	}
	// http base URL needs the egress allow-list.
	if _, err := svc.PutSettings(ctx, PutInput{Provider: ProviderAnthropic, Model: "claude",
		Embeddings: &EmbeddingsPut{BaseURL: "http://127.0.0.1:11434/v1", Model: "e"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("http base url: %v", err)
	}
	// Defaults before the first PUT carry an embeddings view too.
	other := orgCtx(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	if d, err := svc.GetSettings(other); err != nil || d.Embeddings.Configured || d.Embeddings.Problem == "" {
		t.Fatalf("%+v %v", d, err)
	}
}

// embedServer is a fake OpenAI-compatible /embeddings endpoint: the vector of an input is its length and
// first byte, returned in reverse order to check that the client sorts by index.
func embedServer(t *testing.T, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []item
		for i := len(req.Input) - 1; i >= 0; i-- {
			in := req.Input[i]
			data = append(data, item{Index: i, Embedding: []float32{float32(len(in)), float32(in[0])}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": req.Model})
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

func TestOpenAIEmbeddingsClient(t *testing.T) {
	srv, auth := embedServer(t, 0)
	p := &OpenAIEmbeddings{BaseURL: srv.URL + "/v1/", HTTP: srv.Client()}
	vecs, err := p.Embed(context.Background(), "sk-1", "m", []string{"a", "bbb"})
	if err != nil || len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 3 {
		t.Fatalf("%v %v", vecs, err)
	}
	if (*auth)[0] != "Bearer sk-1" {
		t.Fatalf("auth %q", (*auth)[0])
	}
	if _, err := p.Embed(context.Background(), "", "m", []string{"x"}); err != nil || (*auth)[1] != "" {
		t.Fatalf("no key → no Authorization header: %q %v", (*auth)[1], err)
	}
	limited, _ := embedServer(t, http.StatusTooManyRequests)
	_, err = (&OpenAIEmbeddings{BaseURL: limited.URL + "/v1", HTTP: limited.Client()}).Embed(context.Background(), "k", "m", []string{"x"})
	if errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("429: %v", err)
	}
	broken, _ := embedServer(t, http.StatusInternalServerError)
	_, err = (&OpenAIEmbeddings{BaseURL: broken.URL + "/v1", HTTP: broken.Client()}).Embed(context.Background(), "k", "m", []string{"x"})
	if errs.CodeOf(err) != errs.CodeServiceUnavailable || !errs.Retryable(err) {
		t.Fatalf("500: %v", err)
	}
	// The default client goes through the egress policy: loopback is refused.
	_, err = (&OpenAIEmbeddings{BaseURL: srv.URL + "/v1"}).Embed(context.Background(), "k", "m", []string{"x"})
	if errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("loopback must be blocked: %v", err)
	}
}

func TestServiceEmbedAndComplete(t *testing.T) {
	srv, auth := embedServer(t, 0)
	org := store.NewID(store.PrefixOrganization)
	var gotSys, gotUser string
	svc := &Service{
		Repo: &memStore{}, Cipher: testCipher(t), Registry: features.DefaultRegistry(), Env: "test",
		Egress: egress.NewDialer(egress.Policy{AllowHosts: []string{"localhost"}}), HTTP: srv.Client(),
		Providers: map[string]Provider{ProviderAnthropic: &MockProvider{Fn: func(_ context.Context, key, model, sys, user string) (string, error) {
			gotSys, gotUser = sys, user
			return "answer", nil
		}}},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	if _, err := svc.Embed(ctx, org, []string{"x"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unconfigured: %v", err)
	}
	if m, err := svc.EmbeddingsModel(ctx, org); err != nil || m != "" {
		t.Fatalf("%q %v", m, err)
	}
	main, own := "sk-ant", "sk-emb"
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/v1"
	if _, err := svc.PutSettings(ctx, PutInput{Provider: ProviderAnthropic, Model: "claude", APIKey: &main,
		Embeddings: &EmbeddingsPut{BaseURL: base, Model: "emb-1", APIKey: &own}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := svc.EmbeddingsModel(ctx, org); m != "emb-1" {
		t.Fatalf("model %q", m)
	}
	emb, err := svc.Embed(ctx, org, []string{"write to anna@example.com", "zz"})
	if err != nil || emb.Model != "emb-1" || emb.Dims != 2 || len(emb.Vectors) != 2 {
		t.Fatalf("%+v %v", emb, err)
	}
	// piiRedact (default on) hashes the address before it leaves: the first input got longer than the original.
	if emb.Vectors[0][0] == float32(len("write to anna@example.com")) {
		t.Fatal("the address must be redacted before embedding")
	}
	if (*auth)[len(*auth)-1] != "Bearer "+own {
		t.Fatalf("own key not used: %q", (*auth)[len(*auth)-1])
	}
	too := make([]string, MaxEmbedInputs+1)
	if _, err := svc.Embed(ctx, org, too); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("too many inputs: %v", err)
	}
	out, err := svc.Complete(ctx, org, "sys", "mail from bob@example.com", 100)
	if err != nil || out != "answer" || gotSys != "sys" || strings.Contains(gotUser, "bob@example.com") {
		t.Fatalf("%q %v %q", out, err, gotUser)
	}
	if svc.AIEnabled(ctx, org, "") != svc.aiEnabled(ctx, org, "") {
		t.Fatal("AIEnabled mirrors the switch")
	}
}
