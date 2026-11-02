package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAICompatComplete(t *testing.T) {
	p := &OpenAICompat{
		BaseURL: "https://example.test/v1",
		HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer sk" {
				t.Fatal("auth")
			}
			body := `{"choices":[{"message":{"content":" hi "}}]}`
			return &http.Response{
				StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)),
				Header: http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}
	out, err := p.Complete(context.Background(), "sk", "gpt", "sys", "user")
	if err != nil || out != "hi" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestOpenAICompatErrors(t *testing.T) {
	p := &OpenAICompat{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	if _, err := p.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("want error")
	}
	p2 := &OpenAICompat{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`))}, nil
	})}
	if _, err := p2.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("want empty choices error")
	}
}

func TestAnthropicComplete(t *testing.T) {
	p := &Anthropic{
		BaseURL: "https://example.test",
		HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("x-api-key") != "ak" {
				t.Fatal("key")
			}
			body := `{"content":[{"type":"text","text":"ok"}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	}
	out, err := p.Complete(context.Background(), "ak", "claude", "sys", "user")
	if err != nil || out != "ok" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestMockProvider(t *testing.T) {
	m := &MockProvider{}
	out, err := m.Complete(context.Background(), "", "", "", "x")
	if err != nil || out != "echo:x" {
		t.Fatal(out, err)
	}
}
