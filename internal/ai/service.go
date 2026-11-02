package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ConfigStore loads and saves org AI configs.
type ConfigStore interface {
	Get(ctx context.Context, orgID string) (*Config, error)
	Upsert(ctx context.Context, c *Config) error
}

// SwitchSettings reads effective organisation settings (legacy; prefer DB docs via aiEnabled).
type SwitchSettings interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
}

// MessageText loads plain text for AI message operations.
type MessageText interface {
	PlainText(ctx context.Context, accountID, messageID string, maxChars int) (string, error)
}

// Service is the AI plugin façade.
type Service struct {
	Repo     ConfigStore
	Cipher   *crypto.Cipher
	Settings SwitchSettings
	Accounts AccountSwitches
	Messages MessageText
	DB       *store.DB
	Registry *features.Registry
	Env      string
	// Egress dials provider HTTP under the SSRF policy (W35). Nil → secure default.
	Egress *egress.Dialer
	// Providers maps provider name → implementation. Tests inject mocks.
	Providers map[string]Provider
	// Listers maps provider name → model lister (Connect). Tests inject mocks; nil → the HTTP clients.
	Listers map[string]ModelLister
	// HTTP overrides the egress-built client when set (tests).
	HTTP HTTPDoer
	// Enabled overrides feature-switch resolution when non-nil (tests).
	Enabled func(ctx context.Context, org, accountID string) bool
	// Audit records AI operations when set.
	Audit func(ctx context.Context, action, accountID, messageID string)
	// Embedder overrides the embeddings client (tests). Nil → OpenAIEmbeddings through the egress policy.
	Embedder Embedder
	// EmbeddingsChanged is called after PutSettings changed the embedding model (or made the endpoint
	// unusable), so stored vectors of the old model can be deleted (set by the embeddings module, G1).
	EmbeddingsChanged func(ctx context.Context, org string)
}

func (s *Service) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

// GetSettings returns the public view (empty defaults when unset).
func (s *Service) GetSettings(ctx context.Context) (*View, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	c, err := s.Repo.Get(ctx, org)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "ai.get")
	}
	if c == nil {
		v := View{
			Provider: ProviderOpenAI, Features: append([]string(nil), AllFeatures...),
			PIIRedact: true, BodyMaxChars: defaultBodyMax, ProviderKeysSet: []string{},
			Embeddings: Config{Provider: ProviderOpenAI}.embeddingsView(),
		}
		return &v, nil
	}
	v := c.view()
	return &v, nil
}

// PutSettings upserts org AI config.
func (s *Service) PutSettings(ctx context.Context, in PutInput) (*View, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	if err := validatePut(in); err != nil {
		return nil, err
	}
	if err := s.checkBaseURL(in.BaseURL); err != nil {
		return nil, err
	}
	if in.Embeddings != nil {
		if err := s.checkBaseURL(in.Embeddings.BaseURL); err != nil {
			return nil, err
		}
	}
	cur, err := s.Repo.Get(ctx, org)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "ai.put.load")
	}
	if cur == nil {
		cur = &Config{
			ID: org, OrganizationID: org, PIIRedact: true, BodyMaxChars: defaultBodyMax,
			Features: append([]string(nil), AllFeatures...),
		}
	}
	beforeEmb := ""
	if cur.Model != "" {
		beforeEmb = embeddingsFingerprint(cur)
	}
	// A stored key belongs to its provider: switching keeps it aside and restores the key kept for the new
	// provider, so a key is never sent to another provider.
	cur.switchProvider(in.Provider)
	cur.BaseURL, cur.Model = strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.Model)
	if in.Provider == ProviderNone {
		cur.BaseURL, cur.Model = "", ""
	}
	if in.Features != nil {
		cur.Features = uniqueFeatures(in.Features)
	}
	if in.PIIRedact != nil {
		cur.PIIRedact = *in.PIIRedact
	}
	if in.BodyMaxChars != nil {
		cur.BodyMaxChars = *in.BodyMaxChars
	}
	if in.APIKey != nil && in.Provider != ProviderNone {
		key := strings.TrimSpace(*in.APIKey)
		if key == "" {
			cur.APIKey = crypto.EncryptedString{}
		} else {
			es, err := s.Cipher.EncryptString(ctx, org, key, crypto.AAD{
				Collection: Collection, Field: "apiKey", DocID: org,
			})
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "ai.encrypt")
			}
			cur.APIKey = es
		}
	}
	if err := s.applyEmbeddingsPut(ctx, org, cur, in.Embeddings); err != nil {
		return nil, err
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		cur.UpdatedBy = c.Actor()
	}
	if err := s.Repo.Upsert(ctx, cur); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "ai.put")
	}
	if beforeEmb != "" && embeddingsFingerprint(cur) != beforeEmb && s.EmbeddingsChanged != nil {
		s.EmbeddingsChanged(ctx, org)
	}
	v := cur.view()
	return &v, nil
}

func validatePut(in PutInput) error {
	if !validProvider(in.Provider) {
		return errs.New(errs.CodeValidationFailed, "provider must be none, openai, anthropic, gemini, xai or local.")
	}
	if len(strings.TrimSpace(in.Model)) > maxEmbedModelLen {
		return errs.New(errs.CodeValidationFailed, "model is too long.").WithSafe("max", maxEmbedModelLen)
	}
	if in.Provider != ProviderNone && strings.TrimSpace(in.Model) == "" {
		return errs.New(errs.CodeValidationFailed, "model is required.")
	}
	if in.Provider == ProviderLocal && strings.TrimSpace(in.BaseURL) == "" {
		return errs.New(errs.CodeValidationFailed, "baseUrl is required for local providers.")
	}
	if in.BodyMaxChars != nil && (*in.BodyMaxChars < minBodyMax || *in.BodyMaxChars > maxBodyMax) {
		return errs.New(errs.CodeValidationFailed, "bodyMaxChars out of range.").
			WithSafe("min", minBodyMax).WithSafe("max", maxBodyMax)
	}
	for _, f := range in.Features {
		switch f {
		case FeatureSummary, FeatureClassification, FeatureExtraction, FeatureRisk:
		default:
			return errs.New(errs.CodeValidationFailed, "Unknown AI feature.").WithSafe("feature", f)
		}
	}
	return validateEmbeddingsPut(in.Provider, in.Embeddings)
}

func validProvider(p string) bool {
	for _, v := range AllProviders {
		if v == p {
			return true
		}
	}
	return false
}

func uniqueFeatures(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range in {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// Summarize produces a short summary of text for the organisation.
func (s *Service) Summarize(ctx context.Context, org, text string) (string, error) {
	return s.run(ctx, org, "", FeatureSummary, "Summarise the following email text in 3–5 sentences.", text, nil)
}

// Classify picks one of labels for text.
func (s *Service) Classify(ctx context.Context, org, text string, labels []string) (string, error) {
	if len(labels) == 0 {
		return "", errs.New(errs.CodeValidationFailed, "At least one label is required.")
	}
	sys := fmt.Sprintf("Classify the email into exactly one of these labels: %s. Reply with the label only.",
		strings.Join(labels, ", "))
	return s.run(ctx, org, "", FeatureClassification, sys, text, labels)
}

// Extract pulls structured data per a short schema description.
func (s *Service) Extract(ctx context.Context, org, text, schema string) (string, error) {
	if strings.TrimSpace(schema) == "" {
		return "", errs.New(errs.CodeValidationFailed, "schema is required.")
	}
	sys := "Extract structured data as JSON matching this schema description: " + schema + ". Reply with JSON only."
	return s.run(ctx, org, "", FeatureExtraction, sys, text, nil)
}

// Risk assesses message risk (low|medium|high|unknown).
func (s *Service) Risk(ctx context.Context, org, text string) (string, error) {
	sys := "Assess the security/phishing risk of this email. Reply with exactly one word: low, medium, high, or unknown."
	return s.run(ctx, org, "", FeatureRisk, sys, text, []string{"low", "medium", "high", "unknown"})
}

func (s *Service) run(ctx context.Context, org, accountID, feature, system, text string, labels []string) (string, error) {
	if org == "" {
		var ok bool
		org, ok = store.OrganizationFrom(ctx)
		if !ok {
			return "", store.ErrNoTenant
		}
	}
	if !s.aiEnabled(ctx, org, accountID) {
		return "", errs.New(errs.CodeFeatureDisabled, "The AI plugin is disabled for this organisation.")
	}
	cfg, err := s.Repo.Get(ctx, org)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "ai.cfg")
	}
	if cfg != nil && cfg.Provider == ProviderNone {
		return "", errs.New(errs.CodeFeatureDisabled, "No AI provider is selected for this organisation.")
	}
	if cfg == nil || cfg.Model == "" {
		return "", errs.New(errs.CodeValidationFailed, "AI settings are not configured.")
	}
	if !featureAllowed(cfg, feature) {
		return "", errs.New(errs.CodeFeatureDisabled, "This AI feature is not allowed.").WithSafe("feature", feature)
	}
	if cfg.APIKey.IsZero() {
		return "", errs.New(errs.CodeValidationFailed, "AI API key is not configured.")
	}
	key, err := s.Cipher.DecryptString(ctx, cfg.APIKey, crypto.AAD{
		Collection: Collection, Field: "apiKey", DocID: org,
	})
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "ai.decrypt")
	}
	max := cfg.BodyMaxChars
	if max <= 0 {
		max = defaultBodyMax
	}
	var user string
	if cfg.PIIRedact {
		user = RedactText(text, max)
	} else {
		user = Truncate(text, max)
	}
	prov, err := s.providerOf(cfg)
	if err != nil {
		return "", err
	}
	out, err := prov.Complete(ctx, key, cfg.Model, system, user)
	if err != nil {
		return "", err
	}
	if feature == FeatureClassification && len(labels) > 0 {
		out = matchLabel(out, labels)
	}
	return out, nil
}

func featureAllowed(c *Config, feature string) bool {
	feats := c.Features
	if len(feats) == 0 {
		feats = AllFeatures
	}
	for _, f := range feats {
		if f == feature {
			return true
		}
	}
	return false
}

func matchLabel(out string, labels []string) string {
	trim := strings.TrimSpace(out)
	lower := strings.ToLower(trim)
	for _, l := range labels {
		if strings.EqualFold(l, trim) || strings.Contains(lower, strings.ToLower(l)) {
			return l
		}
	}
	return trim
}

// effectiveBaseURL is the base URL a provider is called at: the configured one, else the provider's public
// API (local has none).
func effectiveBaseURL(provider, base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base != "" {
		return base
	}
	switch provider {
	case ProviderOpenAI:
		return defaultOpenAIURL
	case ProviderAnthropic:
		return defaultAnthropicURL
	case ProviderGemini:
		return defaultGeminiURL
	case ProviderXAI:
		return defaultXAIURL
	}
	return ""
}

func (s *Service) providerOf(cfg *Config) (Provider, error) {
	if s.Providers != nil {
		if p, ok := s.Providers[cfg.Provider]; ok {
			return p, nil
		}
	}
	base := effectiveBaseURL(cfg.Provider, cfg.BaseURL)
	if err := s.checkBaseURL(base); err != nil {
		return nil, err
	}
	http := s.httpDoer()
	switch cfg.Provider {
	case ProviderOpenAI, ProviderLocal, ProviderXAI:
		return &OpenAICompat{BaseURL: base, HTTP: http}, nil
	case ProviderAnthropic:
		return &Anthropic{BaseURL: base, HTTP: http}, nil
	case ProviderGemini:
		return &Gemini{BaseURL: base, HTTP: http}, nil
	default:
		return nil, errs.New(errs.CodeValidationFailed, "Unknown AI provider.").WithSafe("provider", cfg.Provider)
	}
}
