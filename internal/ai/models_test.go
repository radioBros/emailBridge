package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// mockProvider is a TLS server standing in for a provider API (never a real provider).
type mockProvider struct {
	t      *testing.T
	srv    *httptest.Server
	hits   []*http.Request
	bodies []string
	status int
}

func newMockProvider(t *testing.T, routes map[string]string) *mockProvider {
	t.Helper()
	m := &mockProvider{t: t}
	m.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.hits = append(m.hits, r.Clone(context.Background()))
		m.bodies = append(m.bodies, string(b))
		if m.status != 0 {
			w.WriteHeader(m.status)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided: sk-abc***xyz"}}`))
			return
		}
		key := r.Method + " " + r.URL.Path
		if r.URL.Query().Get("after_id") != "" || r.URL.Query().Get("pageToken") != "" {
			key += " page2"
		}
		body, ok := routes[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func ids(ms []Model) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return strings.Join(out, ",")
}

func TestOpenAICompatListModels(t *testing.T) {
	m := newMockProvider(t, map[string]string{"GET /v1/models": `{"object":"list","data":[
		{"id":"gpt-4o","object":"model"},{"id":"text-embedding-3-small"},{"id":"whisper-1"},
		{"id":"dall-e-3"},{"id":"gpt-4o-mini"},{"id":"tts-1"},{"id":"omni-moderation-latest"}]}`})
	p := &OpenAICompat{BaseURL: m.srv.URL + "/v1", HTTP: m.srv.Client()}
	chat, err := p.ListModels(context.Background(), "sk-test", ModelKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(chat); got != "gpt-4o,gpt-4o-mini" {
		t.Fatalf("chat models %q", got)
	}
	if h := m.hits[0].Header.Get("Authorization"); h != "Bearer sk-test" {
		t.Fatalf("auth header %q", h)
	}
	emb, err := p.ListModels(context.Background(), "sk-test", ModelKindEmbeddings)
	if err != nil || ids(emb) != "text-embedding-3-small" {
		t.Fatalf("embeddings %v %v", ids(emb), err)
	}
}

func TestOpenAICompatListModelsLocalNamesAndNoKey(t *testing.T) {
	m := newMockProvider(t, map[string]string{"GET /v1/models": `{"data":[{"id":"llama3.2"},{"id":"bge-m3"}]}`})
	p := &OpenAICompat{BaseURL: m.srv.URL + "/v1", HTTP: m.srv.Client()}
	// No embed-named model: every model is offered rather than none.
	emb, err := p.ListModels(context.Background(), "", ModelKindEmbeddings)
	if err != nil || ids(emb) != "llama3.2,bge-m3" {
		t.Fatalf("%v %v", ids(emb), err)
	}
	if h := m.hits[0].Header.Get("Authorization"); h != "" {
		t.Fatalf("no key → no auth header, got %q", h)
	}
}

func TestAnthropicListModelsPaged(t *testing.T) {
	m := newMockProvider(t, map[string]string{
		"GET /v1/models":       `{"data":[{"type":"model","id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"}],"has_more":true,"first_id":"claude-sonnet-4-5","last_id":"claude-sonnet-4-5"}`,
		"GET /v1/models page2": `{"data":[{"type":"model","id":"claude-haiku-4-5","display_name":"Claude Haiku 4.5"}],"has_more":false,"last_id":"claude-haiku-4-5"}`,
	})
	p := &Anthropic{BaseURL: m.srv.URL, HTTP: m.srv.Client()}
	got, err := p.ListModels(context.Background(), "sk-ant", ModelKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Claude Sonnet 4.5" || got[1].ID != "claude-haiku-4-5" {
		t.Fatalf("%+v", got)
	}
	if m.hits[0].Header.Get("x-api-key") != "sk-ant" || m.hits[0].Header.Get("anthropic-version") == "" {
		t.Fatalf("headers %v", m.hits[0].Header)
	}
	if m.hits[1].URL.Query().Get("after_id") != "claude-sonnet-4-5" {
		t.Fatalf("page 2 query %s", m.hits[1].URL.RawQuery)
	}
	if _, err := p.ListModels(context.Background(), "sk-ant", ModelKindEmbeddings); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("anthropic embeddings: %v", err)
	}
}

func TestGeminiListModelsFilters(t *testing.T) {
	m := newMockProvider(t, map[string]string{
		"GET /v1beta/models": `{"models":[
			{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash","supportedGenerationMethods":["generateContent","countTokens"]},
			{"name":"models/gemini-embedding-001","displayName":"Gemini Embedding","supportedGenerationMethods":["embedContent"]}
		],"nextPageToken":"p2"}`,
		"GET /v1beta/models page2": `{"models":[{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent"]}]}`,
	})
	p := &Gemini{BaseURL: m.srv.URL + "/v1beta", HTTP: m.srv.Client()}
	chat, err := p.ListModels(context.Background(), "AIza-test", ModelKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if ids(chat) != "gemini-2.5-flash,gemini-2.5-pro" || chat[1].Name != "gemini-2.5-pro" {
		t.Fatalf("%+v", chat)
	}
	for _, h := range m.hits {
		if h.Header.Get("x-goog-api-key") != "AIza-test" || strings.Contains(h.URL.RawQuery, "key=") {
			t.Fatalf("key must travel in the header only: %s %v", h.URL, h.Header)
		}
	}
	emb, err := p.ListModels(context.Background(), "AIza-test", ModelKindEmbeddings)
	if err != nil || ids(emb) != "gemini-embedding-001" {
		t.Fatalf("%v %v", ids(emb), err)
	}
}

func TestListModelsErrorMapping(t *testing.T) {
	m := newMockProvider(t, nil)
	p := &OpenAICompat{BaseURL: m.srv.URL, HTTP: m.srv.Client()}
	for status, want := range map[int]errs.Code{
		http.StatusUnauthorized: errs.CodeValidationFailed, http.StatusForbidden: errs.CodeValidationFailed,
		http.StatusTooManyRequests: errs.CodeRateLimited, http.StatusBadGateway: errs.CodeServiceUnavailable,
		http.StatusNotFound: errs.CodeValidationFailed,
	} {
		m.status = status
		_, err := p.ListModels(context.Background(), "sk-abc", ModelKindChat)
		if errs.CodeOf(err) != want {
			t.Fatalf("status %d: want %s, got %v", status, want, err)
		}
		if strings.Contains(err.Error(), "sk-abc") || strings.Contains(err.Error(), "Incorrect API key") {
			t.Fatalf("provider body echoed: %v", err)
		}
	}
	m.status = 0
	_, err := (&OpenAICompat{BaseURL: m.srv.URL + "/nope", HTTP: m.srv.Client()}).ListModels(context.Background(), "", ModelKindChat)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("404 %v", err)
	}
	bad := newMockProvider(t, map[string]string{"GET /models": `not json`})
	_, err = (&OpenAICompat{BaseURL: bad.srv.URL, HTTP: bad.srv.Client()}).ListModels(context.Background(), "", ModelKindChat)
	if errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatalf("bad json %v", err)
	}
	// Unreachable host: no URL or key in the message.
	_, err = (&OpenAICompat{BaseURL: "https://127.0.0.1:1", HTTP: m.srv.Client()}).ListModels(context.Background(), "sk-abc", ModelKindChat)
	if errs.CodeOf(err) != errs.CodeServiceUnavailable || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("dial %v", err)
	}
}

func TestGeminiCompleteMapping(t *testing.T) {
	m := newMockProvider(t, map[string]string{
		"POST /v1beta/models/gemini-2.5-flash:generateContent": `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "},{"text":"world"}]},"finishReason":"STOP"}]}`,
	})
	p := &Gemini{BaseURL: m.srv.URL, HTTP: m.srv.Client()}
	out, err := p.Complete(context.Background(), "AIza-k", "models/gemini-2.5-flash", "be brief", "hi there")
	if err != nil || out != "Hello world" {
		t.Fatalf("%q %v", out, err)
	}
	var body struct {
		SystemInstruction struct {
			Parts []struct{ Text string } `json:"parts"`
		} `json:"systemInstruction"`
		Contents []struct {
			Role  string                  `json:"role"`
			Parts []struct{ Text string } `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal([]byte(m.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body.SystemInstruction.Parts[0].Text != "be brief" || body.Contents[0].Role != "user" || body.Contents[0].Parts[0].Text != "hi there" {
		t.Fatalf("%s", m.bodies[0])
	}
	if m.hits[0].Header.Get("x-goog-api-key") != "AIza-k" || m.hits[0].URL.RawQuery != "" {
		t.Fatalf("key placement %v %s", m.hits[0].Header, m.hits[0].URL)
	}
	m.status = http.StatusInternalServerError
	if _, err := p.Complete(context.Background(), "k", "gemini-2.5-flash", "s", "u"); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatal(err)
	}
	if _, err := parseGeminiText([]byte(`{"candidates":[]}`)); err == nil {
		t.Fatal("no candidates")
	}
	if _, err := parseGeminiText([]byte(`{"candidates":[{"content":{"parts":[]}}]}`)); err == nil {
		t.Fatal("no text")
	}
}

func TestGeminiEmbeddingsMapping(t *testing.T) {
	m := newMockProvider(t, map[string]string{
		"POST /v1beta/models/gemini-embedding-001:batchEmbedContents": `{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`,
	})
	e := &GeminiEmbeddings{BaseURL: m.srv.URL, HTTP: m.srv.Client()}
	vecs, err := e.Embed(context.Background(), "AIza-k", "gemini-embedding-001", []string{"a", "b"})
	if err != nil || len(vecs) != 2 || vecs[1][1] != 0.4 {
		t.Fatalf("%v %v", vecs, err)
	}
	if !strings.Contains(m.bodies[0], `"model":"models/gemini-embedding-001"`) || !strings.Contains(m.bodies[0], `"text":"b"`) {
		t.Fatalf("%s", m.bodies[0])
	}
	if _, err := e.Embed(context.Background(), "k", "gemini-embedding-001", []string{"only one"}); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatalf("count mismatch %v", err)
	}
	m.status = http.StatusTooManyRequests
	if _, err := e.Embed(context.Background(), "k", "gemini-embedding-001", []string{"a"}); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatal(err)
	}
}

func TestServiceListModelsStoredKeyPolicy(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	m := newMockProvider(t, map[string]string{
		"GET /models":    `{"data":[{"id":"gpt-4o"},{"id":"text-embedding-3-large"}]}`,
		"GET /v1/models": `{"data":[{"id":"claude-haiku-4-5","display_name":"Claude Haiku 4.5"}],"has_more":false}`,
	})
	svc := &Service{Repo: &memStore{}, Cipher: testCipher(t), Registry: features.DefaultRegistry(), HTTP: m.srv.Client()}
	ctx := orgCtx(org, tenancy.RoleOwner)
	key := "sk-openai-stored"
	if _, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, BaseURL: m.srv.URL, Model: "gpt-4o", APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	// Stored key, same provider and base URL.
	v, err := svc.ListModels(ctx, ModelsInput{Provider: ProviderOpenAI, BaseURL: m.srv.URL})
	if err != nil || ids(v.Models) != "gpt-4o" {
		t.Fatalf("%v %v", v, err)
	}
	if got := m.hits[len(m.hits)-1].Header.Get("Authorization"); got != "Bearer "+key {
		t.Fatalf("stored key not used: %q", got)
	}
	// Another provider at the same host: the OpenAI key is never sent.
	before := len(m.hits)
	if _, err := svc.ListModels(ctx, ModelsInput{Provider: ProviderAnthropic, BaseURL: m.srv.URL}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("want key required, got %v", err)
	}
	if len(m.hits) != before {
		t.Fatal("provider called without a key")
	}
	// Same provider, another base URL: the stored key stays home.
	if _, err := svc.ListModels(ctx, ModelsInput{Provider: ProviderOpenAI, BaseURL: m.srv.URL + "/other"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("other base: %v", err)
	}
	// A typed key works and is not stored.
	v, err = svc.ListModels(ctx, ModelsInput{Provider: ProviderAnthropic, BaseURL: m.srv.URL, APIKey: "sk-ant-typed"})
	if err != nil || v.Models[0].Name != "Claude Haiku 4.5" {
		t.Fatalf("%v %v", v, err)
	}
	if got, _ := svc.GetSettings(ctx); got.Provider != ProviderOpenAI || strings.Join(got.ProviderKeysSet, ",") != "openai" {
		t.Fatalf("listing must not store anything: %+v", got)
	}
	emb, err := svc.ListModels(ctx, ModelsInput{Provider: ProviderOpenAI, BaseURL: m.srv.URL, Kind: ModelKindEmbeddings})
	if err != nil || ids(emb.Models) != "text-embedding-3-large" {
		t.Fatalf("%v %v", emb, err)
	}
	for _, in := range []ModelsInput{
		{Provider: ProviderNone}, {Provider: "bogus"}, {Provider: ProviderLocal},
		{Provider: ProviderOpenAI, Kind: "vision"}, {Provider: ProviderXAI, Kind: ModelKindEmbeddings},
		{Provider: ProviderLocal, BaseURL: "http://10.0.0.5:11434/v1"},
	} {
		if _, err := svc.ListModels(ctx, in); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if _, err := svc.ListModels(orgCtx(org, tenancy.RoleViewer), ModelsInput{Provider: ProviderOpenAI, APIKey: "k"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("member: %v", err)
	}
}

func TestProviderSwitchKeepsKeysApart(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := &memStore{}
	svc := &Service{Repo: st, Cipher: testCipher(t), Registry: features.DefaultRegistry()}
	ctx := orgCtx(org, tenancy.RoleOwner)
	openaiKey := "sk-openai"
	if _, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt-4o", APIKey: &openaiKey}); err != nil {
		t.Fatal(err)
	}
	// Switch without a key: the OpenAI key is kept aside, Gemini has none.
	v, err := svc.PutSettings(ctx, PutInput{Provider: ProviderGemini, Model: "gemini-2.5-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if v.APIKeySet || strings.Join(v.ProviderKeysSet, ",") != "openai" {
		t.Fatalf("after switch: %+v", v)
	}
	geminiKey := "AIza-gemini"
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderGemini, Model: "gemini-2.5-flash", APIKey: &geminiKey}); err != nil || !v.APIKeySet {
		t.Fatal(v, err)
	}
	// None keeps both keys and needs no model.
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderNone}); err != nil || v.APIKeySet || v.Model != "" ||
		strings.Join(v.ProviderKeysSet, ",") != "gemini,openai" {
		t.Fatalf("none: %+v %v", v, err)
	}
	// Back to OpenAI: its own key returns.
	if v, err = svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt-4o"}); err != nil || !v.APIKeySet {
		t.Fatal(v, err)
	}
	cfg, _ := st.Get(context.Background(), org)
	_, prov, key, err := svc.completer(context.Background(), org)
	if err != nil || key != openaiKey {
		t.Fatalf("restored key %q %v", key, err)
	}
	if _, ok := prov.(*OpenAICompat); !ok || cfg.keyFor(ProviderGemini).IsZero() {
		t.Fatalf("provider %T, gemini key kept %v", prov, !cfg.keyFor(ProviderGemini).IsZero())
	}
}

func TestProviderNoneDisablesFeatures(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{
		Repo: &memStore{docs: map[string]*Config{org: {ID: org, Provider: ProviderNone}}}, Cipher: testCipher(t),
		Enabled: func(context.Context, string, string) bool { return true },
	}
	if _, err := svc.Summarize(orgCtx(org, tenancy.RoleOwner), org, "text"); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("summarize: %v", err)
	}
	if _, err := svc.Complete(context.Background(), org, "s", "u", 0); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("complete: %v", err)
	}
	if err := validatePut(PutInput{Provider: ProviderNone}); err != nil {
		t.Fatal(err)
	}
	if err := validatePut(PutInput{Provider: ProviderGemini}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("gemini needs a model")
	}
	if err := validatePut(PutInput{Provider: ProviderXAI, Model: "grok-4"}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderOfDefaults(t *testing.T) {
	svc := &Service{}
	for prov, want := range map[string]string{
		ProviderGemini: defaultGeminiURL, ProviderXAI: defaultXAIURL, ProviderOpenAI: defaultOpenAIURL, ProviderAnthropic: defaultAnthropicURL,
	} {
		p, err := svc.providerOf(&Config{Provider: prov})
		if err != nil {
			t.Fatal(prov, err)
		}
		var got string
		switch x := p.(type) {
		case *Gemini:
			got = x.BaseURL
		case *OpenAICompat:
			got = x.BaseURL
		case *Anthropic:
			got = x.BaseURL
		}
		if got != want {
			t.Fatalf("%s: %q", prov, got)
		}
	}
	if geminiRoot("https://generativelanguage.googleapis.com/v1beta/") != defaultGeminiURL || geminiRoot("") != defaultGeminiURL {
		t.Fatal("gemini root")
	}
}

func TestEmbedTargetPerProvider(t *testing.T) {
	cases := []struct {
		cfg     Config
		kind    string
		problem string
	}{
		{Config{Provider: ProviderGemini}, embedGemini, "Set the embedding model for the Gemini provider."},
		{Config{Provider: ProviderGemini, Embeddings: &EmbeddingsConfig{Model: "gemini-embedding-001"}}, embedGemini, "An API key is required for the Gemini embeddings endpoint."},
		{Config{Provider: ProviderXAI}, embedOpenAI, "The xAI provider has no embeddings API: set an embeddings base URL and model."},
		{Config{Provider: ProviderNone}, embedOpenAI, "No AI provider is selected: set an embeddings base URL and model."},
		{Config{Provider: ProviderXAI, Embeddings: &EmbeddingsConfig{BaseURL: "https://emb.example.com/v1", Model: "m"}}, embedOpenAI, ""},
	}
	for _, c := range cases {
		tg := c.cfg.embedTarget()
		if tg.Kind != c.kind || tg.Problem != c.problem {
			t.Fatalf("%+v → %+v", c.cfg, tg)
		}
	}
	if err := validateEmbeddingsPut(ProviderGemini, &EmbeddingsPut{Model: "gemini-embedding-001"}); err != nil {
		t.Fatal(err)
	}
	if err := validateEmbeddingsPut(ProviderXAI, &EmbeddingsPut{Model: "m"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("xai needs an own base URL")
	}
}
