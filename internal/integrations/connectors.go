package integrations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ZapierDefinition is the generic Zapier/Make catch-hook connector.
func ZapierDefinition() *Definition {
	return &Definition{
		ID: TypeZapier, Version: "1.0.0", Name: "Zapier / Make", Category: CategoryAutomation,
		Icon: "mdi-lightning-bolt", Color: "#FF4A00",
		Description: "Forward EmailBridge events to a Zapier or Make (Integromat) catch hook. " +
			"Full event payloads leave EmailBridge to the automation platform.",
		DocsPath: "/docs/integrations/zapier",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"hookUrl"},
			"properties": map[string]any{
				"hookUrl": map[string]any{
					"type": "string", "format": "uri", "title": "Catch hook URL",
				},
				"filter": map[string]any{
					"type": "string", "title": "Event filter CEL", "default": "true",
				},
			},
		},
		Capabilities: []string{CapTest},
		Resources: ResourceTemplates{
			WebhookEndpoints: []EndpointTemplate{{
				Name: "Zapier/Make hook", URL: "{{config.hookUrl}}", Events: []string{},
			}},
			Routes: []RouteTemplate{{
				Name: "Zapier/Make forward", EndpointIndex: 0,
				Filter: `{{config.filter}}`,
				Map:    "", // passthrough CloudEvents body
			}},
		},
		Test: testWebhookPOST("Zapier/Make", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["hookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("hookUrl required")
			}
			body := []byte(`{"source":"emailbridge","type":"integration.test"}`)
			return u, body, nil
		}),
	}
}

// RspamdDefinition installs spam-scoring settings (sync wiring in P8.3).
func RspamdDefinition() *Definition {
	return &Definition{
		ID: TypeRspamd, Version: "1.0.0", Name: "Rspamd", Category: CategorySpam,
		Icon: "mdi-shield-bug", Color: "#E74C3C",
		Description: "Score inbound mail with Rspamd. When sync scoring is enabled, raw message bytes " +
			"are sent to your Rspamd instance; results appear on message.received.spam.",
		DocsPath: "/docs/integrations/rspamd",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"baseUrl"},
			"properties": map[string]any{
				"baseUrl": map[string]any{
					"type": "string", "format": "uri", "title": "Rspamd base URL",
					"description": "e.g. http://rspamd:11333",
				},
				"timeout": map[string]any{
					"type": "string", "title": "Timeout", "default": "5s",
				},
				"password": map[string]any{
					"type": "string", "writeOnly": true, "title": "Controller password",
				},
			},
		},
		SecretFields: []string{"password"},
		Capabilities: []string{CapTest},
		Resources: ResourceTemplates{
			Settings: map[string]string{
				"integrations.rspamd.baseUrl": "{{config.baseUrl}}",
				"integrations.rspamd.timeout": "{{config.timeout}}",
			},
		},
		Test: func(ctx context.Context, in TestInput) (TestResult, error) {
			base, _ := in.Config["baseUrl"].(string)
			base = strings.TrimRight(strings.TrimSpace(base), "/")
			if base == "" {
				return TestResult{OK: false, Detail: "baseUrl required"}, nil
			}
			client := in.Client
			if client == nil {
				client = http.DefaultClient
			}
			url := base + "/ping"
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return TestResult{OK: false, Detail: err.Error()}, nil
			}
			if pw := strings.TrimSpace(in.Secrets["password"]); pw != "" {
				req.Header.Set("Password", pw)
			}
			res, err := client.Do(req)
			lat := time.Since(start).Milliseconds()
			if err != nil {
				return TestResult{OK: false, LatencyMs: lat, Detail: err.Error()}, nil
			}
			defer func() { _ = res.Body.Close() }()
			_, _ = io.Copy(io.Discard, res.Body)
			ok := res.StatusCode >= 200 && res.StatusCode < 300
			detail := fmt.Sprintf("Rspamd ping HTTP %d", res.StatusCode)
			return TestResult{OK: ok, LatencyMs: lat, Detail: detail}, nil
		},
	}
}

func testWebhookPOST(label string, build func(cfg map[string]any, secrets map[string]string) (url string, body []byte, err error)) func(context.Context, TestInput) (TestResult, error) {
	return func(ctx context.Context, in TestInput) (TestResult, error) {
		u, body, err := build(in.Config, in.Secrets)
		if err != nil {
			return TestResult{OK: false, Detail: err.Error()}, nil
		}
		client := in.Client
		if client == nil {
			client = http.DefaultClient
		}
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return TestResult{OK: false, Detail: err.Error()}, nil
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "EmailBridge-IntegrationTest/1")
		res, err := client.Do(req)
		lat := time.Since(start).Milliseconds()
		if err != nil {
			return TestResult{OK: false, LatencyMs: lat, Detail: err.Error()}, nil
		}
		defer func() { _ = res.Body.Close() }()
		_, _ = io.Copy(io.Discard, res.Body)
		ok := res.StatusCode >= 200 && res.StatusCode < 300
		return TestResult{
			OK: ok, LatencyMs: lat,
			Detail: fmt.Sprintf("%s test HTTP %d", label, res.StatusCode),
		}, nil
	}
}

// SwitchName returns the feature-switch key for a connector id.
func SwitchName(connectorID string) string {
	return "integrations." + connectorID
}
