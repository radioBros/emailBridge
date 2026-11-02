package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Gemini talks to the Google Generative Language API (generateContent). The key travels in the
// x-goog-api-key header, never in the URL, so transport errors (which print the URL) cannot leak it.
type Gemini struct {
	BaseURL string
	HTTP    HTTPDoer
}

// geminiRoot is the API root without a version suffix (a configured ".../v1beta" is accepted too).
func geminiRoot(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = defaultGeminiURL
	}
	for _, suffix := range []string{"/v1beta", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base
}

// geminiModelPath is "models/<id>" escaped for the URL path.
func geminiModelPath(model string) string {
	return "models/" + url.PathEscape(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
}

func (p *Gemini) client() HTTPDoer {
	if p.HTTP != nil {
		return p.HTTP
	}
	return secureDefaultDialer().HTTPClient(providerHTTPTimeout)
}

// Complete implements Provider.
func (p *Gemini) Complete(ctx context.Context, apiKey, model, system, user string) (string, error) {
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": user}}}},
		"generationConfig":  map[string]any{"temperature": 0},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	u := geminiRoot(p.BaseURL) + "/v1beta/" + geminiModelPath(model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeValidationFailed, "ai.gemini.url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	res, err := p.client().Do(req)
	if err != nil {
		if errors.Is(err, egress.ErrBlocked) {
			return "", mapProviderDialErr(err)
		}
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.gemini")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.gemini.read")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned an error.").
			WithSafe("status", res.StatusCode)
	}
	return parseGeminiText(b)
}

func parseGeminiText(b []byte) (string, error) {
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "ai.gemini.decode")
	}
	if len(parsed.Candidates) == 0 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned no candidates.")
	}
	var out strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		out.WriteString(part.Text)
	}
	if out.Len() == 0 {
		return "", errs.New(errs.CodeServiceUnavailable, "The AI provider returned no text.")
	}
	return strings.TrimSpace(out.String()), nil
}

// GeminiEmbeddings talks to POST {root}/v1beta/models/{model}:batchEmbedContents.
type GeminiEmbeddings struct {
	BaseURL string
	HTTP    HTTPDoer
}

// Embed implements Embedder.
func (p *GeminiEmbeddings) Embed(ctx context.Context, apiKey, model string, inputs []string) ([][]float32, error) {
	modelPath := "models/" + strings.TrimPrefix(strings.TrimSpace(model), "models/")
	reqs := make([]map[string]any, len(inputs))
	for i, in := range inputs {
		reqs[i] = map[string]any{"model": modelPath, "content": map[string]any{"parts": []map[string]string{{"text": in}}}}
	}
	raw, err := json.Marshal(map[string]any{"requests": reqs})
	if err != nil {
		return nil, err
	}
	u := geminiRoot(p.BaseURL) + "/v1beta/" + geminiModelPath(model) + ":batchEmbedContents"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "ai.embeddings.url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
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
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "ai.embeddings.decode")
	}
	if len(parsed.Embeddings) != len(inputs) {
		return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned the wrong number of vectors.").
			WithSafe("want", len(inputs)).WithSafe("got", len(parsed.Embeddings))
	}
	out := make([][]float32, len(inputs))
	for i, e := range parsed.Embeddings {
		out[i] = e.Values
	}
	return out, nil
}
