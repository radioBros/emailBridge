package connectors

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TestInput is passed to a connector test (mirrors integrations.TestInput).
type TestInput struct {
	Config  map[string]any
	Secrets map[string]string
	Client  *http.Client
}

// TestResult is the outcome of a connector test.
type TestResult struct {
	OK        bool
	LatencyMs int64
	Detail    string
}

// RunTest executes TestFunc when set, otherwise the named Tester for a Def.
func RunTest(ctx context.Context, d Def, in TestInput) (TestResult, error) {
	if d.TestFunc != nil {
		return d.TestFunc(ctx, in)
	}
	switch d.Tester {
	case TesterSlack:
		return testWebhookJSON(ctx, in, "Slack", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["webhookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("webhookUrl required")
			}
			return u, []byte(`{"text":"EmailBridge Slack integration test","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"*EmailBridge* test"}}]}`), nil
		})
	case TesterTeams:
		return testWebhookJSON(ctx, in, "Teams", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["webhookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("webhookUrl required")
			}
			return u, []byte(`{"@type":"MessageCard","@context":"https://schema.org/extensions","summary":"EmailBridge test","title":"EmailBridge","text":"Teams integration test"}`), nil
		})
	case TesterDiscord:
		return testWebhookJSON(ctx, in, "Discord", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["webhookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("webhookUrl required")
			}
			return u, []byte(`{"content":"EmailBridge Discord integration test"}`), nil
		})
	case TesterMattermost:
		return testWebhookJSON(ctx, in, "Mattermost", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["webhookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("webhookUrl required")
			}
			return u, []byte(`{"text":"EmailBridge Mattermost integration test"}`), nil
		})
	case TesterHelpdesk:
		return testWebhookJSON(ctx, in, "Helpdesk", func(cfg map[string]any, _ map[string]string) (string, []byte, error) {
			u, _ := cfg["webhookUrl"].(string)
			if strings.TrimSpace(u) == "" {
				return "", nil, fmt.Errorf("webhookUrl required")
			}
			return u, []byte(`{"source":"emailbridge","type":"integration.test"}`), nil
		})
	case TesterTelegram:
		return testTelegram(ctx, in)
	case TesterFeatherDesk:
		return testFeatherDesk(ctx, in)
	default:
		return TestResult{OK: true, Detail: "test not supported"}, nil
	}
}

func testTelegram(ctx context.Context, in TestInput) (TestResult, error) {
	token := strings.TrimSpace(in.Secrets["botToken"])
	chatID, _ := in.Config["chatId"].(string)
	if token == "" {
		return TestResult{OK: false, Detail: "botToken required"}, nil
	}
	if strings.TrimSpace(chatID) == "" {
		return TestResult{OK: false, Detail: "chatId required"}, nil
	}
	// Tests inject a mock base via config _apiBase (never used in production installs).
	base, _ := in.Config["_apiBase"].(string)
	if base == "" {
		base = "https://api.telegram.org"
	}
	url := strings.TrimRight(base, "/") + "/bot" + token + "/sendMessage"
	body := []byte(`{"chat_id":"` + chatID + `","text":"EmailBridge Telegram integration test"}`)
	return doJSON(ctx, in.Client, "Telegram", url, body, nil)
}

func testFeatherDesk(ctx context.Context, in TestInput) (TestResult, error) {
	base, _ := in.Config["baseUrl"].(string)
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	key := strings.TrimSpace(in.Secrets["apiKey"])
	if base == "" {
		return TestResult{OK: false, Detail: "baseUrl required"}, nil
	}
	if key == "" {
		return TestResult{OK: false, Detail: "apiKey required"}, nil
	}
	url := base + "/api/v1/tickets"
	body := []byte(`{"subject":"EmailBridge test","requester":"test@example.com","body":"integration test"}`)
	return doJSON(ctx, in.Client, "FeatherDesk", url, body, map[string]string{
		"Authorization": "Bearer " + key,
	})
}

func testWebhookJSON(ctx context.Context, in TestInput, label string, build func(cfg map[string]any, secrets map[string]string) (url string, body []byte, err error)) (TestResult, error) {
	u, body, err := build(in.Config, in.Secrets)
	if err != nil {
		return TestResult{OK: false, Detail: err.Error()}, nil
	}
	return doJSON(ctx, in.Client, label, u, body, nil)
}

func doJSON(ctx context.Context, client *http.Client, label, url string, body []byte, hdr map[string]string) (TestResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return TestResult{OK: false, Detail: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EmailBridge-IntegrationTest/1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
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
