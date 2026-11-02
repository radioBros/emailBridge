package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Embeddings (G1 semantic search): an optional OpenAI-compatible `/embeddings` endpoint in the organisation's
// AI settings. With an empty block and an OpenAI-compatible main provider (openai, local) the main base URL
// and key are reused; Anthropic has no embeddings API, so it needs its own block.

const (
	// DefaultEmbeddingModel is used with the main OpenAI provider when the block names no model.
	DefaultEmbeddingModel = "text-embedding-3-small"
	// MaxEmbedInputs caps the inputs of one Embed call (one provider request).
	MaxEmbedInputs = 64
	// maxEmbedInputChars caps one input (code points) after PII redaction.
	maxEmbedInputChars = 4000
	// maxEmbedDims rejects absurd vectors (the largest common models have 3072 dimensions).
	maxEmbedDims = 8192
	// embedResponseMax bounds the provider response (64 inputs × 3072 floats as JSON ≈ 4 MB).
	embedResponseMax = 32 << 20
	// maxEmbedModelLen bounds the model name.
	maxEmbedModelLen = 200
	// aadEmbeddingsKey is the AAD field of the embeddings API key (distinct from the main key).
	aadEmbeddingsKey = "embeddings.apiKey"
)

// EmbeddingsConfig is the stored embeddings block of Config.
type EmbeddingsConfig struct {
	BaseURL string                 `bson:"baseUrl,omitempty"`
	Model   string                 `bson:"model,omitempty"`
	APIKey  crypto.EncryptedString `bson:"apiKey,omitempty"`
}

// EmbeddingsView is the public shape of the embeddings block (no secret).
type EmbeddingsSettingsView struct {
	BaseURL          string `json:"baseUrl,omitempty" doc:"Own embeddings base URL (OpenAI-compatible, the client posts to {baseUrl}/embeddings); empty = the main provider's" example:"https://api.openai.com/v1"`
	Model            string `json:"model,omitempty" doc:"Embedding model set in the block" example:"text-embedding-3-small"`
	APIKeySet        bool   `json:"apiKeySet" doc:"An own embeddings API key is stored (never returned); without one the main key is used when the main provider is reused" example:"false"`
	Configured       bool   `json:"configured" doc:"An embeddings endpoint is usable with the current settings" example:"true"`
	EffectiveBaseURL string `json:"effectiveBaseUrl,omitempty" doc:"The base URL semantic search calls" example:"https://api.openai.com/v1"`
	EffectiveModel   string `json:"effectiveModel,omitempty" doc:"The embedding model semantic search uses; changing it deletes the stored vectors" example:"text-embedding-3-small"`
	Problem          string `json:"problem,omitempty" doc:"Why the endpoint is not usable (when configured is false)" example:"The Anthropic provider has no embeddings API: set an embeddings base URL and model."`
}

// EmbeddingsPut replaces the embeddings block (PUT /v1/ai/settings).
type EmbeddingsPut struct {
	BaseURL string  `json:"baseUrl,omitempty" doc:"Own OpenAI-compatible embeddings base URL; empty = reuse the main provider (openai, local)" example:"http://ollama.internal:11434/v1"`
	Model   string  `json:"model,omitempty" doc:"Embedding model; required with an own base URL or the local provider (default text-embedding-3-small with openai)" example:"nomic-embed-text"`
	APIKey  *string `json:"apiKey,omitempty" doc:"Own embeddings API key; stored encrypted, never returned. Omit to keep, empty string to remove" example:"sk-REPLACE-WITH-YOUR-KEY"`
}

// Embedding API flavours of a resolved endpoint.
const (
	embedOpenAI = "openai" // POST {base}/embeddings (OpenAI and compatible servers)
	embedGemini = "gemini" // POST {base}/v1beta/models/{model}:batchEmbedContents
)

// embedTarget is the resolved embeddings endpoint of a config.
type embedTarget struct {
	Kind     string // embedOpenAI or embedGemini
	BaseURL  string
	Model    string
	Key      crypto.EncryptedString
	KeyField string // AAD field of Key
	Problem  string // non-empty when the endpoint is not usable
}

// embedTarget resolves the endpoint: an own base URL wins; otherwise the main provider when it is
// OpenAI-compatible.
func (c *Config) embedTarget() embedTarget {
	if c == nil {
		return embedTarget{Problem: "AI settings are not configured."}
	}
	var e EmbeddingsConfig
	if c.Embeddings != nil {
		e = *c.Embeddings
	}
	t := embedTarget{Kind: embedOpenAI, Model: e.Model}
	if !e.APIKey.IsZero() {
		t.Key, t.KeyField = e.APIKey, aadEmbeddingsKey
	}
	if e.BaseURL != "" {
		t.BaseURL = e.BaseURL
		if t.Model == "" {
			t.Problem = "Set the embedding model for the embeddings base URL."
		}
		return t
	}
	switch c.Provider {
	case ProviderOpenAI:
		t.BaseURL = c.BaseURL
		if t.BaseURL == "" {
			t.BaseURL = defaultOpenAIURL
		}
		if t.Model == "" {
			t.Model = DefaultEmbeddingModel
		}
		if t.Key.IsZero() {
			t.Key, t.KeyField = c.APIKey, "apiKey"
		}
		if t.Key.IsZero() {
			t.Problem = "An API key is required for the OpenAI embeddings endpoint."
		}
	case ProviderLocal:
		t.BaseURL = c.BaseURL
		if t.Key.IsZero() && !c.APIKey.IsZero() {
			t.Key, t.KeyField = c.APIKey, "apiKey"
		}
		switch {
		case t.BaseURL == "":
			t.Problem = "Set the base URL of the local provider."
		case t.Model == "":
			t.Problem = "Set the embedding model for the local provider."
		}
	case ProviderGemini:
		t.Kind = embedGemini
		t.BaseURL = effectiveBaseURL(ProviderGemini, c.BaseURL)
		if t.Key.IsZero() {
			t.Key, t.KeyField = c.APIKey, "apiKey"
		}
		switch {
		case t.Model == "":
			t.Problem = "Set the embedding model for the Gemini provider."
		case t.Key.IsZero():
			t.Problem = "An API key is required for the Gemini embeddings endpoint."
		}
	case ProviderXAI:
		t.Problem = "The xAI provider has no embeddings API: set an embeddings base URL and model."
	case ProviderNone, "":
		t.Problem = "No AI provider is selected: set an embeddings base URL and model."
	default:
		t.Problem = "The Anthropic provider has no embeddings API: set an embeddings base URL and model."
	}
	return t
}

// hasEmbeddingsAPI reports whether the main provider can embed without an own embeddings base URL.
func hasEmbeddingsAPI(provider string) bool {
	switch provider {
	case ProviderOpenAI, ProviderLocal, ProviderGemini:
		return true
	}
	return false
}

func (c Config) embeddingsView() EmbeddingsSettingsView {
	v := EmbeddingsSettingsView{}
	if c.Embeddings != nil {
		v.BaseURL, v.Model, v.APIKeySet = c.Embeddings.BaseURL, c.Embeddings.Model, !c.Embeddings.APIKey.IsZero()
	}
	t := c.embedTarget()
	v.Configured = t.Problem == ""
	v.Problem = t.Problem
	if v.Configured {
		v.EffectiveBaseURL, v.EffectiveModel = t.BaseURL, t.Model
	}
	return v
}

// validateEmbeddingsPut checks the block of a PUT against the provider of the same PUT.
func validateEmbeddingsPut(provider string, e *EmbeddingsPut) error {
	if e == nil {
		return nil
	}
	model := strings.TrimSpace(e.Model)
	if len(model) > maxEmbedModelLen {
		return errs.New(errs.CodeValidationFailed, "embeddings.model is too long.").WithSafe("max", maxEmbedModelLen)
	}
	base := strings.TrimSpace(e.BaseURL)
	if base != "" && model == "" {
		return errs.New(errs.CodeValidationFailed, "embeddings.model is required with embeddings.baseUrl.")
	}
	if !hasEmbeddingsAPI(provider) && base == "" && model != "" {
		return errs.New(errs.CodeValidationFailed,
			"embeddings.baseUrl is required with this provider (it has no embeddings API).").WithSafe("provider", provider)
	}
	return nil
}

// applyEmbeddingsPut merges the block of a PUT into the stored config (the key: omit = keep, "" = remove).
func (s *Service) applyEmbeddingsPut(ctx context.Context, org string, cur *Config, e *EmbeddingsPut) error {
	if e == nil {
		return nil
	}
	next := EmbeddingsConfig{BaseURL: strings.TrimSpace(e.BaseURL), Model: strings.TrimSpace(e.Model)}
	if cur.Embeddings != nil {
		next.APIKey = cur.Embeddings.APIKey
	}
	if e.APIKey != nil {
		key := strings.TrimSpace(*e.APIKey)
		if key == "" {
			next.APIKey = crypto.EncryptedString{}
		} else {
			es, err := s.Cipher.EncryptString(ctx, org, key, crypto.AAD{Collection: Collection, Field: aadEmbeddingsKey, DocID: org})
			if err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "ai.encrypt")
			}
			next.APIKey = es
		}
	}
	if next.BaseURL == "" && next.Model == "" && next.APIKey.IsZero() {
		cur.Embeddings = nil
		return nil
	}
	cur.Embeddings = &next
	return nil
}

// embeddingsFingerprint is what makes stored vectors comparable: the model of a usable endpoint ("" when
// none). The base URL is not part of it: the same model behind another gateway gives the same vectors.
func embeddingsFingerprint(c *Config) string {
	t := c.embedTarget()
	if t.Problem != "" {
		return ""
	}
	return t.Model
}

// Embedding is the result of Service.Embed.
type Embedding struct {
	Model   string
	Dims    int
	Vectors [][]float32
}

// EmbeddingsModel returns the embedding model semantic search uses for the organisation, or "" when no
// embeddings endpoint is usable.
func (s *Service) EmbeddingsModel(ctx context.Context, org string) (string, error) {
	cfg, err := s.Repo.Get(ctx, org)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "ai.cfg")
	}
	if cfg == nil {
		return "", nil
	}
	return embeddingsFingerprint(cfg), nil
}

// Embed turns inputs into vectors with the organisation's embeddings endpoint (one provider request, at most
// MaxEmbedInputs inputs). Inputs are redacted like every other AI call when piiRedact is on and capped at
// 4000 characters. The caller checks the switches. Errors: validation_failed when no endpoint is usable,
// service_unavailable / rate_limited from the provider, endpoint_url_blocked from the egress policy.
func (s *Service) Embed(ctx context.Context, org string, inputs []string) (*Embedding, error) {
	if len(inputs) == 0 {
		return &Embedding{}, nil
	}
	if len(inputs) > MaxEmbedInputs {
		return nil, errs.New(errs.CodeValidationFailed, "Too many inputs for one embeddings request.").WithSafe("max", MaxEmbedInputs)
	}
	cfg, err := s.Repo.Get(ctx, org)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "ai.cfg")
	}
	t := cfg.embedTarget()
	if t.Problem != "" {
		return nil, errs.New(errs.CodeValidationFailed, "Embeddings are not configured: "+t.Problem)
	}
	key := ""
	if !t.Key.IsZero() {
		key, err = s.Cipher.DecryptString(ctx, t.Key, crypto.AAD{Collection: Collection, Field: t.KeyField, DocID: org})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "ai.decrypt")
		}
	}
	in := make([]string, len(inputs))
	for i, text := range inputs {
		if cfg.PIIRedact {
			in[i] = RedactText(text, maxEmbedInputChars)
		} else {
			in[i] = Truncate(text, maxEmbedInputChars)
		}
	}
	emb := s.Embedder
	if emb == nil {
		if err := s.checkBaseURL(t.BaseURL); err != nil {
			return nil, err
		}
		if t.Kind == embedGemini {
			emb = &GeminiEmbeddings{BaseURL: t.BaseURL, HTTP: s.httpDoer()}
		} else {
			emb = &OpenAIEmbeddings{BaseURL: t.BaseURL, HTTP: s.httpDoer()}
		}
	}
	vecs, err := emb.Embed(ctx, key, t.Model, in)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(in) {
		return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned the wrong number of vectors.").
			WithSafe("want", len(in)).WithSafe("got", len(vecs))
	}
	dims := len(vecs[0])
	for _, v := range vecs {
		if len(v) == 0 || len(v) != dims || dims > maxEmbedDims {
			return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned malformed vectors.").WithSafe("dims", len(v))
		}
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned malformed vectors.")
			}
		}
	}
	return &Embedding{Model: t.Model, Dims: dims, Vectors: vecs}, nil
}

// Complete sends one system + user prompt to the organisation's main provider and returns the reply. It is
// for features built on the AI settings that have their own switch (G1 ask); the caller checks the
// switches. The user text is redacted when piiRedact is on and capped at maxChars (≤ 0: bodyMaxChars).
func (s *Service) Complete(ctx context.Context, org, system, user string, maxChars int) (string, error) {
	cfg, prov, key, err := s.completer(ctx, org)
	if err != nil {
		return "", err
	}
	if maxChars <= 0 {
		maxChars = cfg.BodyMaxChars
	}
	if cfg.PIIRedact {
		user = RedactText(user, maxChars)
	} else {
		user = Truncate(user, maxChars)
	}
	return prov.Complete(ctx, key, cfg.Model, system, user)
}

// AIEnabled reports whether the `ai.enabled` switch is on for the account (or the organisation when
// accountID is empty).
func (s *Service) AIEnabled(ctx context.Context, org, accountID string) bool {
	return s.aiEnabled(ctx, org, accountID)
}

// completer loads what a completion needs: the config, the provider and the decrypted key.
func (s *Service) completer(ctx context.Context, org string) (*Config, Provider, string, error) {
	cfg, err := s.Repo.Get(ctx, org)
	if err != nil {
		return nil, nil, "", errs.Wrap(err, errs.CodeInternalError, "ai.cfg")
	}
	if cfg != nil && cfg.Provider == ProviderNone {
		return nil, nil, "", errs.New(errs.CodeFeatureDisabled, "No AI provider is selected for this organisation.")
	}
	if cfg == nil || cfg.Model == "" {
		return nil, nil, "", errs.New(errs.CodeValidationFailed, "AI settings are not configured.")
	}
	if cfg.APIKey.IsZero() {
		return nil, nil, "", errs.New(errs.CodeValidationFailed, "AI API key is not configured.")
	}
	key, err := s.Cipher.DecryptString(ctx, cfg.APIKey, crypto.AAD{Collection: Collection, Field: "apiKey", DocID: org})
	if err != nil {
		return nil, nil, "", errs.Wrap(err, errs.CodeInternalError, "ai.decrypt")
	}
	prov, err := s.providerOf(cfg)
	if err != nil {
		return nil, nil, "", err
	}
	return cfg, prov, key, nil
}

// Embedder turns texts into vectors. Tests inject mocks — never hit a real API in CI.
type Embedder interface {
	Embed(ctx context.Context, apiKey, model string, inputs []string) ([][]float32, error)
}

// OpenAIEmbeddings talks to an OpenAI-compatible POST {base}/embeddings (OpenAI, compatible gateways,
// Ollama, LM Studio).
type OpenAIEmbeddings struct {
	BaseURL string
	HTTP    HTTPDoer
}

// Embed implements Embedder.
func (p *OpenAIEmbeddings) Embed(ctx context.Context, apiKey, model string, inputs []string) ([][]float32, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIURL
	}
	raw, err := json.Marshal(map[string]any{"model": model, "input": inputs, "encoding_format": "float"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "ai.embeddings.url")
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	c := p.HTTP
	if c == nil {
		c = secureDefaultDialer().HTTPClient(providerHTTPTimeout)
	}
	res, err := c.Do(req)
	if err != nil {
		if errors.Is(err, egress.ErrBlocked) {
			return nil, mapProviderDialErr(err)
		}
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "ai.embeddings")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, embedResponseMax))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "ai.embeddings.read")
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, errs.New(errs.CodeRateLimited, "The embeddings provider is rate limiting requests.").WithSafe("status", res.StatusCode)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned an error.").WithSafe("status", res.StatusCode)
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "ai.embeddings.decode")
	}
	out := make([][]float32, len(inputs))
	for i, d := range parsed.Data {
		idx := d.Index
		if idx == 0 && i > 0 && out[0] != nil { // servers that omit index: keep the order
			idx = i
		}
		if idx < 0 || idx >= len(out) || out[idx] != nil {
			return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned an unexpected index.")
		}
		out[idx] = d.Embedding
	}
	for _, v := range out {
		if v == nil {
			return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned too few vectors.")
		}
	}
	return out, nil
}
