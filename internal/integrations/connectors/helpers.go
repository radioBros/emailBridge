// Package connectors holds catalogue definitions for P8.2/P8.3.
// It must not import internal/integrations (parent materialises Def → Definition).
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

// HTTPTester builds a TestFunc that POSTs/GETs a provider URL (used by P8.3 sending/storage).
func HTTPTester(label string, build func(cfg map[string]any, secrets map[string]string) (method, url string, headers map[string]string, body []byte, err error)) func(context.Context, TestInput) (TestResult, error) {
	return func(ctx context.Context, in TestInput) (TestResult, error) {
		method, u, headers, body, err := build(in.Config, in.Secrets)
		if err != nil {
			return TestResult{OK: false, Detail: err.Error()}, nil
		}
		client := in.Client
		if client == nil {
			client = http.DefaultClient
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return TestResult{OK: false, Detail: err.Error()}, nil
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
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

func strCfg(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg[key].(string)
	return strings.TrimSpace(v)
}

func sendingSchema(extra map[string]any, required ...string) map[string]any {
	props := map[string]any{
		"from": map[string]any{"type": "string", "title": "Default From", "format": "email"},
	}
	for k, v := range extra {
		props[k] = v
	}
	req := make([]any, 0, len(required))
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{"type": "object", "required": req, "properties": props}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
