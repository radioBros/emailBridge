package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/sdk/go/client"
)

func TestPatchPutAndDefaultHeaders(t *testing.T) {
	t.Parallel()
	var method, reqID, extra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		reqID = r.Header.Get("X-Request-Id")
		extra = r.Header.Get("X-Extra")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
	}))
	t.Cleanup(srv.Close)

	h := http.Header{}
	h.Set("X-Extra", "yes")
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", Header: h})

	var out map[string]string
	hdr := http.Header{}
	hdr.Set("X-Request-Id", "req_abc12345")
	if err := c.Patch(context.Background(), "/v1/x", &out, client.RequestOptions{Header: hdr, Body: map[string]int{"n": 1}}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPatch || reqID != "req_abc12345" || extra != "yes" {
		t.Fatalf("method=%s reqID=%s extra=%s", method, reqID, extra)
	}
	if err := c.Put(context.Background(), "/v1/x", &out, client.RequestOptions{Body: map[string]int{"n": 2}}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut {
		t.Fatalf("method=%s", method)
	}
}

func TestRetry503HTTPDateAndExhausted(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	when := time.Now().UTC().Add(-time.Second).Format(http.TimeFormat)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Retry-After", when)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"type":"t","title":"Unavailable","status":503,"code":"unavailable","kind":"temporary"}`))
		_ = n
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", MaxRetries: 2})
	err := c.Get(context.Background(), "/v1/down", nil, client.RequestOptions{})
	pe, ok := err.(*client.ProblemError)
	if !ok || pe.Problem.Code != "unavailable" {
		t.Fatalf("%T %v", err, err)
	}
	if hits.Load() != 3 { // initial + 2 retries
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestParseProblemFallbackAndErrorString(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`not-json`))
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k"})
	err := c.Get(context.Background(), "/v1/x", nil, client.RequestOptions{})
	pe, ok := err.(*client.ProblemError)
	if !ok {
		t.Fatalf("%T", err)
	}
	if pe.Problem.Status != 502 || pe.Error() == "" {
		t.Fatalf("%+v", pe.Problem)
	}
}

func TestStreamEventsErrors(t *testing.T) {
	t.Parallel()
	c := client.New(client.Options{BaseURL: "http://127.0.0.1:1", APIKey: "k"})
	if err := c.StreamEvents(context.Background(), client.StreamEventsOptions{}); err == nil {
		t.Fatal("expected OnEvent required")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(srv.Close)
	c2 := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", OrganizationID: "org_x"})
	err := c2.StreamEvents(context.Background(), client.StreamEventsOptions{
		Types:     "message.received",
		AccountID: "acc_1",
		OnEvent:   func(client.SSEEvent) error { return nil },
	})
	if err == nil {
		t.Fatal("expected status error")
	}
}

func TestStreamEventsCallbackError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: x\n\n"))
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k"})
	boom := errors.New("boom")
	err := c.StreamEvents(context.Background(), client.StreamEventsOptions{
		OnEvent: func(client.SSEEvent) error { return boom },
	})
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}

func TestIterateCursorError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"t","title":"err","status":500,"code":"internal","kind":"temporary"}`))
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k"})
	var saw error
	for _, err := range client.IterateCursor[map[string]any](context.Background(), c, "/v1/accounts", client.IterateOptions{}) {
		saw = err
		break
	}
	if saw == nil {
		t.Fatal("expected error")
	}
}

func TestOrgNotOverridden(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("EB-Organization")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k", OrganizationID: "org_default"})
	hdr := http.Header{}
	hdr.Set("EB-Organization", "org_override")
	_ = c.Get(context.Background(), "/v1/x", nil, client.RequestOptions{Header: hdr})
	if got != "org_override" {
		t.Fatalf("%q", got)
	}
}
