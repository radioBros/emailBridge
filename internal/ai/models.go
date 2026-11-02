package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Model kinds of a list-models request.
const (
	ModelKindChat       = "chat"
	ModelKindEmbeddings = "embeddings"
)

const (
	listModelsTimeout = 15 * time.Second
	listModelsMax     = 500     // models returned at most
	listModelsPages   = 5       // provider pages followed at most
	listModelsBodyMax = 4 << 20 // bytes read per page
)

// Model is one entry of a provider's model list.
type Model struct {
	ID   string `json:"id" doc:"Model ID to put in the settings" example:"claude-haiku-4-5"`
	Name string `json:"name" doc:"Display name (the ID when the provider has none)" example:"Claude Haiku 4.5"`
}

// ModelLister lists the models a provider offers. Tests use mock HTTP servers — never a real API.
type ModelLister interface {
	ListModels(ctx context.Context, apiKey, kind string) ([]Model, error)
}

// ModelsInput is POST /v1/ai/models.
type ModelsInput struct {
	Provider string `json:"provider" enum:"openai,anthropic,gemini,xai,local" doc:"Provider to connect to" example:"anthropic"`
	BaseURL  string `json:"baseUrl,omitempty" doc:"API base URL; required for local, default the provider's public API" example:"https://llm.internal.example.com/v1"`
	APIKey   string `json:"apiKey,omitempty" doc:"Key to try; omit to use the key stored for this provider and base URL. Never stored by this call" example:"sk-ant-api03-REPLACE-WITH-YOUR-KEY"`
	Kind     string `json:"kind,omitempty" enum:"chat,embeddings" doc:"chat (default) lists models for the AI features, embeddings the embedding models for semantic search" example:"chat"`
}

// ModelsView is the response of POST /v1/ai/models.
type ModelsView struct {
	Models []Model `json:"models" doc:"Models the provider offers for the requested kind, sorted by ID"`
}

// ListModels verifies the key against the provider and returns its models (the admin's Connect button).
// The stored key is used only for the provider (and base URL) it was saved for, so a key never reaches
// another provider. Errors: validation_failed (bad input, missing key, key refused by the provider),
// service_unavailable / rate_limited from the provider, endpoint_url_blocked from the egress policy.
func (s *Service) ListModels(ctx context.Context, in ModelsInput) (*ModelsView, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	kind := in.Kind
	if kind == "" {
		kind = ModelKindChat
	}
	if kind != ModelKindChat && kind != ModelKindEmbeddings {
		return nil, errs.New(errs.CodeValidationFailed, "kind must be chat or embeddings.")
	}
	if in.Provider == ProviderNone || !validProvider(in.Provider) {
		return nil, errs.New(errs.CodeValidationFailed, "provider must be openai, anthropic, gemini, xai or local.")
	}
	if kind == ModelKindEmbeddings && !hasEmbeddingsAPI(in.Provider) {
		return nil, errs.New(errs.CodeValidationFailed, "This provider has no embeddings API.").WithSafe("provider", in.Provider)
	}
	base := effectiveBaseURL(in.Provider, in.BaseURL)
	if base == "" {
		return nil, errs.New(errs.CodeValidationFailed, "baseUrl is required for local providers.")
	}
	if err := s.checkBaseURL(base); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		var err error
		if key, err = s.storedKeyFor(ctx, org, in.Provider, base, kind); err != nil {
			return nil, err
		}
	}
	if key == "" && in.Provider != ProviderLocal {
		return nil, errs.New(errs.CodeValidationFailed, "An API key is required to connect to this provider.")
	}
	lister := s.listerOf(in.Provider, base)
	cctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()
	models, err := lister.ListModels(cctx, key, kind)
	if err != nil {
		return nil, err
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if len(models) > listModelsMax {
		models = models[:listModelsMax]
	}
	return &ModelsView{Models: models}, nil
}

// storedKeyFor decrypts the key saved for provider at base ("" when none matches).
func (s *Service) storedKeyFor(ctx context.Context, org, provider, base, kind string) (string, error) {
	cfg, err := s.Repo.Get(ctx, org)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "ai.cfg")
	}
	if cfg == nil {
		return "", nil
	}
	var enc crypto.EncryptedString
	field := "apiKey"
	if e := cfg.Embeddings; kind == ModelKindEmbeddings && e != nil && e.BaseURL != "" && sameURL(e.BaseURL, base) && !e.APIKey.IsZero() {
		enc, field = e.APIKey, aadEmbeddingsKey
	} else {
		stored := effectiveBaseURL(provider, "")
		if provider == cfg.Provider {
			stored = effectiveBaseURL(provider, cfg.BaseURL)
		}
		if stored == "" || !sameURL(stored, base) {
			return "", nil
		}
		enc = cfg.keyFor(provider)
	}
	if enc.IsZero() {
		return "", nil
	}
	key, err := s.Cipher.DecryptString(ctx, enc, crypto.AAD{Collection: Collection, Field: field, DocID: org})
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "ai.decrypt")
	}
	return key, nil
}

func sameURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(strings.TrimSpace(a), "/"), strings.TrimRight(strings.TrimSpace(b), "/"))
}

func (s *Service) listerOf(provider, base string) ModelLister {
	if s.Listers != nil {
		if l, ok := s.Listers[provider]; ok {
			return l
		}
	}
	h := s.httpDoer()
	switch provider {
	case ProviderAnthropic:
		return &Anthropic{BaseURL: base, HTTP: h}
	case ProviderGemini:
		return &Gemini{BaseURL: base, HTTP: h}
	default: // openai, xai, local: OpenAI-compatible
		return &OpenAICompat{BaseURL: base, HTTP: h}
	}
}

// getModelsPage GETs one list page and maps provider failures to problems without echoing the provider's
// body (it may repeat a masked key).
func getModelsPage(ctx context.Context, c HTTPDoer, u string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "AI baseUrl is not a valid absolute URL.")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	res, err := c.Do(req)
	if err != nil {
		if errors.Is(err, egress.ErrBlocked) {
			return mapProviderDialErr(err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return errs.New(errs.CodeServiceUnavailable, "The AI provider did not answer in time.")
		}
		return errs.New(errs.CodeServiceUnavailable, "The AI provider could not be reached.")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, listModelsBodyMax))
	if err != nil {
		return errs.New(errs.CodeServiceUnavailable, "The AI provider's answer could not be read.")
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return errs.New(errs.CodeValidationFailed, "The AI provider rejected the API key.").WithSafe("status", res.StatusCode)
	case res.StatusCode == http.StatusTooManyRequests:
		return errs.New(errs.CodeRateLimited, "The AI provider is rate limiting requests.").WithSafe("status", res.StatusCode)
	case res.StatusCode == http.StatusNotFound:
		return errs.New(errs.CodeValidationFailed, "The AI provider has no model list at this base URL.").WithSafe("status", res.StatusCode)
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return errs.New(errs.CodeServiceUnavailable, "The AI provider returned an error.").WithSafe("status", res.StatusCode)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errs.New(errs.CodeServiceUnavailable, "The AI provider returned an unexpected model list.")
	}
	return nil
}

// nonChatMarkers are id fragments of OpenAI-compatible models that cannot chat (the list API has no
// capability field).
var nonChatMarkers = []string{"embed", "whisper", "tts", "dall-e", "moderation", "transcribe", "image", "audio", "realtime", "search", "davinci", "babbage"}

// ListModels implements ModelLister (GET {base}/models: OpenAI, xAI, Ollama, LM Studio, gateways).
func (p *OpenAICompat) ListModels(ctx context.Context, apiKey, kind string) ([]Model, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIURL
	}
	hdr := map[string]string{}
	if apiKey != "" {
		hdr["Authorization"] = "Bearer " + apiKey
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getModelsPage(ctx, p.client(), base+"/models", hdr, &parsed); err != nil {
		return nil, err
	}
	var all, picked []Model
	for _, d := range parsed.Data {
		if d.ID == "" {
			continue
		}
		m := Model{ID: d.ID, Name: d.ID}
		all = append(all, m)
		lower := strings.ToLower(d.ID)
		isEmbed := strings.Contains(lower, "embed")
		switch kind {
		case ModelKindEmbeddings:
			if isEmbed {
				picked = append(picked, m)
			}
		default:
			if !containsAny(lower, nonChatMarkers) {
				picked = append(picked, m)
			}
		}
	}
	// Local servers name models freely (bge-m3, llama3): when the heuristic finds nothing, show them all.
	if len(picked) == 0 {
		picked = all
	}
	if picked == nil {
		picked = []Model{}
	}
	return picked, nil
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// ListModels implements ModelLister (GET {base}/v1/models, paged by after_id).
func (p *Anthropic) ListModels(ctx context.Context, apiKey, kind string) ([]Model, error) {
	if kind == ModelKindEmbeddings {
		return nil, errs.New(errs.CodeValidationFailed, "This provider has no embeddings API.")
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultAnthropicURL
	}
	c := p.HTTP
	if c == nil {
		c = secureDefaultDialer().HTTPClient(providerHTTPTimeout)
	}
	hdr := map[string]string{"x-api-key": apiKey, "anthropic-version": "2023-06-01"}
	out := []Model{}
	after := ""
	for page := 0; page < listModelsPages; page++ {
		q := url.Values{"limit": {"1000"}}
		if after != "" {
			q.Set("after_id", after)
		}
		var parsed struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := getModelsPage(ctx, c, base+"/v1/models?"+q.Encode(), hdr, &parsed); err != nil {
			return nil, err
		}
		for _, d := range parsed.Data {
			if d.ID == "" {
				continue
			}
			name := d.DisplayName
			if name == "" {
				name = d.ID
			}
			out = append(out, Model{ID: d.ID, Name: name})
		}
		if !parsed.HasMore || parsed.LastID == "" || len(out) >= listModelsMax {
			break
		}
		after = parsed.LastID
	}
	return out, nil
}

// ListModels implements ModelLister (GET {root}/v1beta/models, paged by pageToken; filtered by
// supportedGenerationMethods).
func (p *Gemini) ListModels(ctx context.Context, apiKey, kind string) ([]Model, error) {
	want := []string{"generateContent"}
	if kind == ModelKindEmbeddings {
		want = []string{"embedContent", "batchEmbedContents"}
	}
	hdr := map[string]string{"x-goog-api-key": apiKey}
	out := []Model{}
	token := ""
	for page := 0; page < listModelsPages; page++ {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var parsed struct {
			Models []struct {
				Name                       string   `json:"name"`
				DisplayName                string   `json:"displayName"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := getModelsPage(ctx, p.client(), geminiRoot(p.BaseURL)+"/v1beta/models?"+q.Encode(), hdr, &parsed); err != nil {
			return nil, err
		}
		for _, m := range parsed.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			if id == "" || !anyOf(m.SupportedGenerationMethods, want) {
				continue
			}
			name := m.DisplayName
			if name == "" {
				name = id
			}
			out = append(out, Model{ID: id, Name: name})
		}
		if parsed.NextPageToken == "" || len(out) >= listModelsMax {
			break
		}
		token = parsed.NextPageToken
	}
	return out, nil
}

func anyOf(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}
