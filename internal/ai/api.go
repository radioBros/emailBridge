package ai

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// Register adds AI settings HTTP routes.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P7")
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAISettings", Method: http.MethodGet,
		Path: "/v1/ai/settings", Summary: "Get organisation AI settings",
		Description: "Returns the organisation's AI provider settings: provider (none, openai, anthropic, gemini, xai or " +
			"local), base " +
			"URL, model, the allowed features, PII redaction and the text limit. The API key is never returned; " +
			"`apiKeySet` says whether one is stored. Before the first PUT the defaults are returned. Requires the " +
			"`read` permission (API keys `settings:read`). Read-only.",
		Metadata:  httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: exampleAISettings()}}}},
		Tags:      []string{"ai"}, Extensions: httpapi.WithScope(ext, "settings:read"),
	}, s.getSettings)
	httpapi.Register(srv, huma.Operation{
		OperationID: "putAISettings", Method: http.MethodPut,
		Path: "/v1/ai/settings", Summary: "Upsert organisation AI settings",
		Description: "Creates or replaces the organisation's AI provider settings. `apiKey` is stored encrypted and " +
			"never returned; omit it to keep the stored key, send an empty string to remove it. `features` limits which " +
			"operations may call the provider (default all four); `bodyMaxChars` (256-100000, default 8000) caps the " +
			"text sent per call. A `local` provider needs `baseUrl` (an OpenAI-compatible server); `none` turns the " +
			"AI features off and needs no model. Keys are kept per provider: switching provider keeps the old key " +
			"aside (never sent to the new provider) and restores the key kept for the new one. Provider calls go " +
			"through the egress policy. The AI operations also need the `ai.enabled` switch. `embeddings` (semantic " +
			"search, switch `ai.embeddings`) names an OpenAI-compatible `/embeddings` endpoint: `baseUrl` and `model`, " +
			"plus an own `apiKey` (same keep / remove rule); omit the block to keep it. Without a base URL an openai or " +
			"local main provider is reused (default model `text-embedding-3-small` for openai); with gemini the " +
			"Gemini embeddings API is used (set `model`); anthropic, xai and none need the block. Changing the embedding model deletes the stored vectors. Requires " +
			"`settings.write` (owner, admin; API keys `settings:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "provider must be none, openai, anthropic, gemini, xai or local."}),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{ //nolint:gosec // G101: documentation placeholder apiKey, not a real secret.
			"provider": "anthropic", "model": "claude-haiku-4-5", "apiKey": "sk-ant-api03-REPLACE-WITH-YOUR-KEY",
			"features": []any{"summary", "classification", "risk"}, "piiRedact": true, "bodyMaxChars": 8000}}}},
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: exampleAISettings()}}}},
		Tags:      []string{"ai"}, Extensions: httpapi.WithScope(ext, "settings:write"),
	}, s.putSettings)
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAIModels", Method: http.MethodPost,
		Path: "/v1/ai/models", Summary: "List the models of an AI provider",
		Description: "Connects to an AI provider with an API key and returns the models it offers, for the model " +
			"picker of the AI settings. `provider` is openai, anthropic, gemini, xai or local (a custom " +
			"OpenAI-compatible server, which needs `baseUrl`). `kind` chat (default) lists the models the AI " +
			"features can use; embeddings lists embedding models for semantic search (OpenAI, Gemini and local " +
			"only). Without `apiKey` the key stored for the same provider and base URL is used; a key is never " +
			"sent to another provider, and this call stores nothing. The provider call goes through the egress " +
			"policy with a 15 s timeout; a key the provider refuses gives `422 validation_failed`, other provider " +
			"failures `503 service_unavailable` (the provider's own error text is never returned). Requires " +
			"`settings.write` (owner, admin; API keys `settings:write`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The AI provider rejected the API key."},
			httpapi.ProblemDoc{Code: errs.CodeEndpointURLBlocked},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider could not be reached."}),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{ //nolint:gosec // G101: documentation placeholder apiKey, not a real secret.
			"provider": "anthropic", "apiKey": "sk-ant-api03-REPLACE-WITH-YOUR-KEY", "kind": "chat"}}}},
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"models": []any{
				map[string]any{"id": "claude-haiku-4-5", "name": "Claude Haiku 4.5"},
				map[string]any{"id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5"},
			}}}}}},
		Tags: []string{"ai"}, Extensions: httpapi.WithScope(httpapi.Phase("P10"), "settings:write"),
	}, s.postModels)
	msgExt := httpapi.WithScope(ext, "messages:read")
	httpapi.Register(srv, huma.Operation{
		OperationID: "aiSummarizeMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/{messageId}/ai/summarize",
		Description: "Summarises a stored message with the organisation's AI provider. " +
			"Needs the `ai.enabled` switch, configured AI settings with an API key and the feature in the " +
			"allow-list (`403 feature_disabled` otherwise). Up to `bodyMaxChars` of the message text are sent to the " +
			"configured provider (with e-mail addresses replaced by hashes when `piiRedact` is on, the default); nothing is " +
			"stored. Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`). " +
			"Writes an audit record (ai.summarize).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The AI plugin is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The message does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "AI settings are not configured."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider returned an error."}),
		Summary: "Summarise a message", Tags: []string{"ai"}, Extensions: msgExt,
	}, s.postSummarize)
	httpapi.Register(srv, huma.Operation{
		OperationID: "aiClassifyMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/{messageId}/ai/classify",
		Description: "Asks the AI provider to put a stored message into one of the given `labels`. " +
			"Needs the `ai.enabled` switch, configured AI settings with an API key and the feature in the " +
			"allow-list (`403 feature_disabled` otherwise). Up to `bodyMaxChars` of the message text are sent to the " +
			"configured provider (with e-mail addresses replaced by hashes when `piiRedact` is on, the default); nothing is " +
			"stored. Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`). " +
			"Writes an audit record (ai.classify).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The AI plugin is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The message does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "At least one label is required."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider returned an error."}),
		Summary: "Classify a message", Tags: []string{"ai"}, Extensions: msgExt,
	}, s.postClassify)
	httpapi.Register(srv, huma.Operation{
		OperationID: "aiExtractMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/{messageId}/ai/extract",
		Description: "Asks the AI provider to extract structured data described by `schema` from a stored message; " +
			"`data` is the model's JSON text, so validate it before use. " +
			"Needs the `ai.enabled` switch, configured AI settings with an API key and the feature in the " +
			"allow-list (`403 feature_disabled` otherwise). Up to `bodyMaxChars` of the message text are sent to the " +
			"configured provider (with e-mail addresses replaced by hashes when `piiRedact` is on, the default); nothing is " +
			"stored. Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`). " +
			"Writes an audit record (ai.extract).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The AI plugin is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The message does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "schema is required."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider returned an error."}),
		Summary: "Extract structured data from a message", Tags: []string{"ai"}, Extensions: msgExt,
	}, s.postExtract)
	httpapi.Register(srv, huma.Operation{
		OperationID: "aiRiskMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/{messageId}/ai/risk",
		Description: "Asks the AI provider to rate the phishing / security risk of a stored message (low, medium, " +
			"high or unknown) with a short rationale. A hint for people, not a spam filter verdict. " +
			"Needs the `ai.enabled` switch, configured AI settings with an API key and the feature in the " +
			"allow-list (`403 feature_disabled` otherwise). Up to `bodyMaxChars` of the message text are sent to the " +
			"configured provider (with e-mail addresses replaced by hashes when `piiRedact` is on, the default); nothing is " +
			"stored. Requires the `read` permission (API keys `messages:read` or `messages:read:<accountId>`). " +
			"Writes an audit record (ai.risk).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The AI plugin is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The message does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "AI settings are not configured."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The AI provider returned an error."}),
		Summary: "Assess message risk", Tags: []string{"ai"}, Extensions: msgExt,
	}, s.postRisk)
}

type settingsOut struct {
	Body View
}

func (s *Service) getSettings(ctx context.Context, _ *struct{}) (*settingsOut, error) {
	v, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOut{Body: *v}, nil
}

type putIn struct {
	Body PutInput
}

func (s *Service) putSettings(ctx context.Context, in *putIn) (*settingsOut, error) {
	v, err := s.PutSettings(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &settingsOut{Body: *v}, nil
}

type modelsIn struct {
	Body ModelsInput
}

type modelsOut struct {
	Body ModelsView
}

func (s *Service) postModels(ctx context.Context, in *modelsIn) (*modelsOut, error) {
	v, err := s.ListModels(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &modelsOut{Body: *v}, nil
}

type msgPath struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID string `path:"messageId" doc:"Message ID (msg_…)." example:"msg_01m5ch2yg09swc93g54trswp15"`
}

type summarizeOut struct {
	Body struct {
		Summary string `json:"summary" doc:"Short summary of the message in its own language" example:"Giulia Bianchi confirms the appointment on 4 November at 10:00 and attaches her ID card."`
	}
}

func (s *Service) postSummarize(ctx context.Context, in *msgPath) (*summarizeOut, error) {
	sum, err := s.SummarizeMessage(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return nil, err
	}
	out := &summarizeOut{}
	out.Body.Summary = sum
	return out, nil
}

type classifyIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID string `path:"messageId" doc:"Message ID (msg_…)." example:"msg_01m5ch2yg09swc93g54trswp15"`
	Body      struct {
		Labels []string `json:"labels" doc:"The labels to choose from (at least one)" example:"[\"appointment\",\"invoice\",\"complaint\",\"other\"]"`
	}
}

type classifyOut struct {
	Body struct {
		Label string `json:"label" doc:"The label the model chose, one of the requested labels" example:"appointment"`
	}
}

func (s *Service) postClassify(ctx context.Context, in *classifyIn) (*classifyOut, error) {
	label, err := s.ClassifyMessage(ctx, in.AccountID, in.MessageID, in.Body.Labels)
	if err != nil {
		return nil, err
	}
	out := &classifyOut{}
	out.Body.Label = label
	return out, nil
}

type extractIn struct {
	AccountID string `path:"accountId" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID string `path:"messageId" doc:"Message ID (msg_…)." example:"msg_01m5ch2yg09swc93g54trswp15"`
	Body      struct {
		Schema string `json:"schema" doc:"Plain-language or JSON Schema description of the data to extract" example:"{\"invoiceNumber\": string, \"total\": number, \"dueDate\": \"YYYY-MM-DD\"}"`
	}
}

type extractOut struct {
	Body struct {
		Data string `json:"data" doc:"The extracted data as the model returned it (JSON text; parse and validate it)" example:"{\"invoiceNumber\": \"2026-118\", \"total\": 1250.00, \"dueDate\": \"2026-11-30\"}"`
	}
}

func (s *Service) postExtract(ctx context.Context, in *extractIn) (*extractOut, error) {
	data, err := s.ExtractMessage(ctx, in.AccountID, in.MessageID, in.Body.Schema)
	if err != nil {
		return nil, err
	}
	out := &extractOut{}
	out.Body.Data = data
	return out, nil
}

type riskOut struct {
	Body struct {
		Risk      string `json:"risk" enum:"low,medium,high,unknown" doc:"Phishing / security risk" example:"high"`
		Rationale string `json:"rationale,omitempty" doc:"Short reason given by the model" example:"The sender domain imitates the bank and the link points to an unrelated host."`
	}
}

func (s *Service) postRisk(ctx context.Context, in *msgPath) (*riskOut, error) {
	risk, rationale, err := s.RiskMessage(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return nil, err
	}
	out := &riskOut{}
	out.Body.Risk = risk
	out.Body.Rationale = rationale
	return out, nil
}
