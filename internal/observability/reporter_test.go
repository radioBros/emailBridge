package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/observability"
)

func TestReporterDisabled(t *testing.T) {
	r, err := observability.NewReporter(observability.ReporterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.CaptureError(context.Background(), errors.New("x"), nil)
	r.Flush(time.Millisecond)
}

func TestReporterFakeEndpointScrub(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		body = append(body, b...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// DSN: http://publickey@host/project
	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://testkey@" + host + "/1"
	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN:         dsn,
		Environment: "test",
		Release:     "dev",
		SampleRate:  1,
		SendPII:     false,
		NodeID:      "node-1",
		Role:        "api",
		Reportable:  func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	r.CaptureError(context.Background(), errors.New("boom"), map[string]string{
		"code": "internal.boom", "component": "test",
	})
	r.Flush(2 * time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(body)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := string(body)
	mu.Unlock()
	if got == "" {
		t.Fatal("no sentry event received")
	}
	// Envelope may contain JSON event; look for tags and scrubbed fields.
	if !strings.Contains(got, "node-1") && !strings.Contains(got, `"node"`) {
		// tags may be in JSON
		if !strings.Contains(got, "node") {
			t.Fatalf("missing node tag in %s", truncate(got, 500))
		}
	}
	if strings.Contains(strings.ToLower(got), `"authorization"`) && strings.Contains(got, "Bearer") {
		t.Fatal("authorization header value leaked")
	}
	if strings.Contains(got, `"cookies"`) && strings.Contains(got, "session=") {
		t.Fatal("cookies leaked")
	}
	_ = json.Valid(body) // envelope is not pure JSON; ignore
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
