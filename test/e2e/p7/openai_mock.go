//go:build e2e

package p7

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// openAIMock is a minimal OpenAI-compatible Chat Completions fake.
type openAIMock struct {
	URL      string
	mu       sync.Mutex
	Calls    int
	LastSys  string
	LastUser string
	Fail     bool
	srv      *httptest.Server
}

func startOpenAIMock(t *testing.T) *openAIMock {
	t.Helper()
	m := &openAIMock{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.Calls++
		if m.Fail {
			http.Error(w, `{"error":"down"}`, http.StatusBadGateway)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		for _, msg := range req.Messages {
			switch msg.Role {
			case "system":
				m.LastSys = msg.Content
			case "user":
				m.LastUser = msg.Content
			}
		}
		content := replyFor(m.LastSys)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": content}},
			},
		})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	m.URL = localhostBaseURL(m.srv.URL)
	return m
}

func replyFor(sys string) string {
	s := strings.ToLower(sys)
	switch {
	case strings.Contains(s, "risk") || strings.Contains(s, "phishing"):
		return "low\nlooks fine"
	case strings.Contains(s, "classify"):
		// Prefer a label that appears in the prompt.
		for _, lab := range []string{"support", "sales", "spam", "urgent", "general", "ham"} {
			if strings.Contains(s, lab) {
				return lab
			}
		}
		return "general"
	case strings.Contains(s, "extract") || strings.Contains(s, "json"):
		return `{"ok":true}`
	case strings.Contains(s, "summar"):
		return "Short summary of the email."
	default:
		return "ok"
	}
}
