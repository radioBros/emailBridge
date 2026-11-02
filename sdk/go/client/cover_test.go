package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/sdk/go/client"
)

func TestProblemErrorMessageFallbacks(t *testing.T) {
	t.Parallel()
	titleOnly := &client.ProblemError{Problem: client.Problem{Title: "T", Code: "c"}}
	if titleOnly.Error() != "T" {
		t.Fatal(titleOnly.Error())
	}
	codeOnly := &client.ProblemError{Problem: client.Problem{Code: "only_code"}}
	if codeOnly.Error() != "only_code" {
		t.Fatal(codeOnly.Error())
	}
}

func TestRetryCancelDuringSleep(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"t","title":"rl","status":429,"code":"rate_limited","kind":"rate_limited"}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", MaxRetries: 3})
	err := c.Get(ctx, "/v1/slow", nil, client.RequestOptions{})
	if err == nil {
		t.Fatal("expected cancel")
	}
}

func TestRetryBackoffWithoutRetryAfter(t *testing.T) {
	t.Parallel()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"t","title":"rl","status":429,"code":"rate_limited","kind":"rate_limited"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", MaxRetries: 1})
	if err := c.Get(context.Background(), "/v1/x", nil, client.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits=%d", hits)
	}
}

func TestStreamEventsCommentAndEmpty(t *testing.T) {
	t.Parallel()
	var gotOrg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrg = r.Header.Get("EB-Organization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": keep-alive\n\n\nevent: ping\ndata: {}\n\n"))
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", OrganizationID: "org_1"})
	var n int
	err := c.StreamEvents(context.Background(), client.StreamEventsOptions{
		OnEvent: func(ev client.SSEEvent) error {
			n++
			if ev.Event != "ping" {
				t.Fatalf("%+v", ev)
			}
			return nil
		},
	})
	if err != nil || n != 1 || gotOrg != "org_1" {
		t.Fatalf("err=%v n=%d org=%q", err, n, gotOrg)
	}
}
