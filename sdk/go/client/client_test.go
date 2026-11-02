package client_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/radiobros/emailbridge/sdk/go/client"
)

func TestClientAuthAndOrgHeaders(t *testing.T) {
	t.Parallel()
	var gotAuth, gotOrg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOrg = r.Header.Get("EB-Organization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "nextCursor": nil})
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test", OrganizationID: "org_1"})
	var page client.Page[map[string]any]
	if err := c.Get(context.Background(), "/v1/accounts", &page, client.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer ebk_test" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotOrg != "org_1" {
		t.Fatalf("org=%q", gotOrg)
	}
}

func TestClientIdempotencyKey(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "acc_new"})
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test"})
	var out map[string]string
	if err := c.Post(context.Background(), "/v1/accounts", &out, client.RequestOptions{
		Body:           map[string]any{},
		IdempotencyKey: "idem-1",
	}); err != nil {
		t.Fatal(err)
	}
	if got != "idem-1" {
		t.Fatalf("idem=%q", got)
	}
}

func TestClientProblemError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"type":"https://emailbridge.dev/problems/validation_failed",
			"title":"Validation failed",
			"status":422,
			"code":"validation_failed",
			"kind":"user",
			"detail":"bad"
		}`))
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test"})
	err := c.Get(context.Background(), "/v1/boom", nil, client.RequestOptions{})
	pe, ok := err.(*client.ProblemError)
	if !ok {
		t.Fatalf("got %T %v", err, err)
	}
	if pe.Problem.Code != "validation_failed" || pe.Problem.Detail != "bad" || pe.StatusCode() != 422 {
		t.Fatalf("%+v", pe.Problem)
	}
}

func TestClientRetry429(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "0")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"t","title":"Rate limited","status":429,"code":"rate_limited","kind":"rate_limited"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test", MaxRetries: 3})
	var out map[string]bool
	if err := c.Get(context.Background(), "/v1/slow", &out, client.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if !out["ok"] || hits.Load() != 3 {
		t.Fatalf("out=%v hits=%d", out, hits.Load())
	}
}

func TestIterateCursor(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("cursor")
		if cur == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":       []map[string]string{{"id": "acc_1"}},
				"nextCursor": "c2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       []map[string]string{{"id": "acc_2"}},
			"nextCursor": nil,
		})
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test"})
	var ids []string
	for item, err := range client.IterateCursor[map[string]string](context.Background(), c, "/v1/accounts", client.IterateOptions{Limit: 10}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item["id"])
	}
	if len(ids) != 2 || ids[0] != "acc_1" || ids[1] != "acc_2" {
		t.Fatalf("%v", ids)
	}
}

func TestListPage(t *testing.T) {
	t.Parallel()
	var gotPage, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPage = r.URL.Query().Get("page")
		gotLimit = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "nextCursor": nil, "total": 0})
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test"})
	_, err := client.ListPage[map[string]any](context.Background(), c, "/v1/accounts", 2, client.IterateOptions{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if gotPage != "2" || gotLimit != "25" {
		t.Fatalf("page=%s limit=%s", gotPage, gotLimit)
	}
}

func TestStreamEvents(t *testing.T) {
	t.Parallel()
	var gotLast string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLast = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt_1\nevent: message.received\ndata: {\"ok\":true}\n\n")
		_, _ = io.WriteString(w, "data: line1\ndata: line2\n\n")
	}))
	t.Cleanup(srv.Close)

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ebk_test", OrganizationID: "org_1"})
	var events []client.SSEEvent
	err := c.StreamEvents(context.Background(), client.StreamEventsOptions{
		LastEventID: "evt_0",
		OnEvent: func(ev client.SSEEvent) error {
			events = append(events, ev)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotLast != "evt_0" {
		t.Fatalf("Last-Event-ID=%q", gotLast)
	}
	if len(events) != 2 {
		t.Fatalf("events=%v", events)
	}
	if events[0].ID != "evt_1" || events[0].Event != "message.received" || events[0].Data != `{"ok":true}` {
		t.Fatalf("%+v", events[0])
	}
	if events[1].Data != "line1\nline2" {
		t.Fatalf("%+v", events[1])
	}
}

func TestNoContent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "k"})
	if err := c.Delete(context.Background(), "/v1/x", nil, client.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
}
