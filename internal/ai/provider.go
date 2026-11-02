package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Provider talks to one LLM backend. Tests inject mocks — never hit a real API in CI.
type Provider interface {
	Complete(ctx context.Context, apiKey, model, system, user string) (string, error)
}

// HTTPDoer is the subset of *http.Client used by providers.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// OpenAICompat talks to OpenAI-compatible chat completions.
type OpenAICompat struct {
	BaseURL string
	HTTP    HTTPDoer
}

func (p *OpenAICompat) Complete(ctx context.Context, apiKey, model, system, user string) (string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIURL
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return readOpenAI(p.client(), req)
}

func (p *OpenAICompat) client() HTTPDoer {
	if p.HTTP != nil {
		return p.HTTP
	}
	// No inject → SSRF-safe default (never http.DefaultClient).
	return secureDefaultDialer().HTTPClient(providerHTTPTimeout)
}

func readOpenAI(c HTTPDoer, req *http.Request) (string, error) {
	res, err := c.Do(req)
	if err != nil {
		if errors.Is(err, egress.ErrBlocked) {
			return "", mapProviderDialErr(err)
		}
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.openai")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.openai.read")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned an error.").
			WithSafe("status", res.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.openai.decode")
	}
	if len(parsed.Choices) == 0 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned no choices.")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// Anthropic talks to the Anthropic Messages API.
type Anthropic struct {
	BaseURL string
	HTTP    HTTPDoer
}

func (p *Anthropic) Complete(ctx context.Context, apiKey, model, system, user string) (string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultAnthropicURL
	}
	body := map[string]any{
		"model": model, "max_tokens": 1024, "system": system,
		"messages": []map[string]string{{"role": "user", "content": user}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	c := p.HTTP
	if c == nil {
		c = secureDefaultDialer().HTTPClient(providerHTTPTimeout)
	}
	res, err := c.Do(req)
	if err != nil {
		if errors.Is(err, egress.ErrBlocked) {
			return "", mapProviderDialErr(err)
		}
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.anthropic")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.anthropic.read")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned an error.").
			WithSafe("status", res.StatusCode)
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.anthropic.decode")
	}
	var out strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" || c.Type == "" {
			out.WriteString(c.Text)
		}
	}
	if out.Len() == 0 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned no text.")
	}
	return strings.TrimSpace(out.String()), nil
}

// MockProvider is a test double; Complete returns a fixed string or error.
type MockProvider struct {
	Fn func(ctx context.Context, apiKey, model, system, user string) (string, error)
}

func (m *MockProvider) Complete(ctx context.Context, apiKey, model, system, user string) (string, error) {
	if m.Fn == nil {
		return fmt.Sprintf("echo:%s", user), nil
	}
	return m.Fn(ctx, apiKey, model, system, user)
}
