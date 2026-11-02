package ai

// OpenAPI examples of the AI operations (W70). Documentation only.

func exampleAISettings() map[string]any {
	return map[string]any{"provider": "anthropic", "model": "claude-haiku-4-5",
		"features": []any{"summary", "classification", "risk"}, "piiRedact": true, "bodyMaxChars": 8000,
		"apiKeySet":       true,
		"providerKeysSet": []string{"anthropic", "openai"},
		"embeddings": map[string]any{"baseUrl": "https://api.openai.com/v1", "model": "text-embedding-3-small", "apiKeySet": true,
			"configured": true, "effectiveBaseUrl": "https://api.openai.com/v1", "effectiveModel": "text-embedding-3-small"},
		"updatedAt": "2026-10-25T10:12:00Z"}
}
