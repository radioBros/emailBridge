package ai

import (
	"sort"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collection holds one AI config document per organisation.
const Collection = "aiConfigs"

// Feature names (allow-list).
const (
	FeatureSummary        = "summary"
	FeatureClassification = "classification"
	FeatureExtraction     = "extraction"
	FeatureRisk           = "risk"
)

// AllFeatures is the default allow-list when empty.
var AllFeatures = []string{FeatureSummary, FeatureClassification, FeatureExtraction, FeatureRisk}

// Providers. `local` is the custom / local OpenAI-compatible server (Ollama, LM Studio, gateways); `none`
// turns the AI features off without losing the stored settings.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderXAI       = "xai"
	ProviderLocal     = "local"
	ProviderNone      = "none"
)

// AllProviders lists the accepted provider values (the order the admin shows them).
var AllProviders = []string{ProviderNone, ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderXAI, ProviderLocal}

const (
	defaultBodyMax      = 8000
	minBodyMax          = 256
	maxBodyMax          = 100_000
	switchAI            = "ai.enabled"
	defaultOpenAIURL    = "https://api.openai.com/v1"
	defaultAnthropicURL = "https://api.anthropic.com"
	defaultGeminiURL    = "https://generativelanguage.googleapis.com"
	defaultXAIURL       = "https://api.x.ai/v1"
)

// Config is the stored org AI settings document.
type Config struct {
	ID             string                 `bson:"_id" json:"-"`
	OrganizationID string                 `bson:"organizationId" json:"-"`
	Provider       string                 `bson:"provider" json:"provider"`
	BaseURL        string                 `bson:"baseUrl,omitempty" json:"baseUrl,omitempty"`
	Model          string                 `bson:"model" json:"model"`
	APIKey         crypto.EncryptedString `bson:"apiKey,omitempty" json:"-"`
	// ProviderKeys keeps the keys of the providers that are not selected, so switching provider never sends
	// one provider's key to another and switching back needs no new key (same AAD as APIKey).
	ProviderKeys map[string]crypto.EncryptedString `bson:"providerKeys,omitempty" json:"-"`
	Features     []string                          `bson:"features,omitempty" json:"features"`
	PIIRedact    bool                              `bson:"piiRedact" json:"piiRedact"`
	BodyMaxChars int                               `bson:"bodyMaxChars" json:"bodyMaxChars"`
	// Embeddings is the optional embeddings endpoint (G1 semantic search); nil = reuse the main provider when
	// it is OpenAI-compatible.
	Embeddings *EmbeddingsConfig `bson:"embeddings,omitempty" json:"-"`
	UpdatedAt  time.Time         `bson:"updatedAt" json:"updatedAt"`
	UpdatedBy  string            `bson:"updatedBy,omitempty" json:"-"`
}

// View is the public API shape (no secret).
type View struct {
	Provider     string   `json:"provider" enum:"none,openai,anthropic,gemini,xai,local" doc:"none (AI features off), openai, anthropic, gemini (Google Gemini), xai (Grok) or local (a custom OpenAI-compatible server)" example:"anthropic"`
	BaseURL      string   `json:"baseUrl,omitempty" doc:"API base URL (required for local; default the provider's public API)" example:"https://llm.internal.example.com/v1"`
	Model        string   `json:"model" doc:"Model name passed to the provider" example:"claude-haiku-4-5"`
	Features     []string `json:"features" doc:"Allowed operations: summary, classification, extraction, risk"`
	PIIRedact    bool     `json:"piiRedact" doc:"E-mail addresses in the text are replaced by hashes before it is sent" example:"true"`
	BodyMaxChars int      `json:"bodyMaxChars" doc:"Maximum characters of message text sent per call" example:"8000"`
	APIKeySet    bool     `json:"apiKeySet" doc:"An API key is stored for the selected provider (it is never returned)" example:"true"`
	// ProviderKeysSet names every provider with a stored key (the selected one included).
	ProviderKeysSet []string `json:"providerKeysSet" doc:"Providers with a stored API key; switching to one of them needs no new key" example:"[\"anthropic\",\"openai\"]"`
	// Embeddings is the embeddings endpoint used by semantic search (G1).
	Embeddings EmbeddingsSettingsView `json:"embeddings" doc:"Embeddings endpoint for semantic search and ask (switch ai.embeddings)"`
	UpdatedAt  time.Time              `json:"updatedAt,omitempty" doc:"When the settings last changed" example:"2026-10-25T10:12:00Z"`
}

// PutInput is PUT /v1/ai/settings.
type PutInput struct {
	Provider     string   `json:"provider" enum:"none,openai,anthropic,gemini,xai,local" doc:"none, openai, anthropic, gemini, xai or local" example:"anthropic"`
	BaseURL      string   `json:"baseUrl,omitempty" doc:"API base URL; required for local" example:"https://llm.internal.example.com/v1"`
	Model        string   `json:"model,omitempty" doc:"Model name; required unless provider is none" example:"claude-haiku-4-5"`
	APIKey       *string  `json:"apiKey,omitempty" doc:"API key of the selected provider; stored encrypted, never returned. Omit to keep the key stored for that provider, empty string to remove it" example:"sk-ant-api03-REPLACE-WITH-YOUR-KEY"`
	Features     []string `json:"features,omitempty" doc:"Allowed operations (default all): summary, classification, extraction, risk"`
	PIIRedact    *bool    `json:"piiRedact,omitempty" doc:"Hash e-mail addresses before sending (default true)" example:"true"`
	BodyMaxChars *int     `json:"bodyMaxChars,omitempty" doc:"Characters of message text sent per call, 256-100000 (default 8000)" example:"8000"`
	// Embeddings replaces the embeddings block; omit to keep it.
	Embeddings *EmbeddingsPut `json:"embeddings,omitempty" doc:"Embeddings endpoint for semantic search (OpenAI-compatible /embeddings). Omit to keep the stored block"`
}

func (c Config) view() View {
	feats := c.Features
	if len(feats) == 0 {
		feats = append([]string(nil), AllFeatures...)
	}
	return View{
		Provider: c.Provider, BaseURL: c.BaseURL, Model: c.Model, Features: feats,
		PIIRedact: c.PIIRedact, BodyMaxChars: c.BodyMaxChars, APIKeySet: !c.APIKey.IsZero(),
		ProviderKeysSet: c.providerKeysSet(),
		Embeddings:      c.embeddingsView(), UpdatedAt: c.UpdatedAt,
	}
}

// providerKeysSet lists the providers with a stored key, sorted.
func (c Config) providerKeysSet() []string {
	out := []string{}
	if !c.APIKey.IsZero() && c.Provider != "" && c.Provider != ProviderNone {
		out = append(out, c.Provider)
	}
	for p, k := range c.ProviderKeys {
		if !k.IsZero() && p != c.Provider {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// keyFor returns the stored key of provider (the selected one or a kept one).
func (c *Config) keyFor(provider string) crypto.EncryptedString {
	if c == nil || provider == "" || provider == ProviderNone {
		return crypto.EncryptedString{}
	}
	if provider == c.Provider {
		return c.APIKey
	}
	return c.ProviderKeys[provider]
}

// switchProvider keeps the key of the old provider and restores the key kept for the new one.
func (c *Config) switchProvider(next string) {
	if c.Provider == next {
		return
	}
	if c.Provider != "" && c.Provider != ProviderNone && !c.APIKey.IsZero() {
		if c.ProviderKeys == nil {
			c.ProviderKeys = map[string]crypto.EncryptedString{}
		}
		c.ProviderKeys[c.Provider] = c.APIKey
	}
	c.APIKey = crypto.EncryptedString{}
	if k, ok := c.ProviderKeys[next]; ok {
		c.APIKey = k
		delete(c.ProviderKeys, next)
	}
	if len(c.ProviderKeys) == 0 {
		c.ProviderKeys = nil
	}
	c.Provider = next
}
