package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/observability"
)

func TestCaptureErrorIncludesTraceID(t *testing.T) {
	var (
		mu   sync.Mutex
		body string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		body += string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN: "http://k@" + host + "/1", SampleRate: 1,
		Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := logging.With(context.Background(), func(id *logging.IDs) {
		id.TraceID = "trace-w51-aaa"
		id.RequestID = "req_w51"
	})
	r.CaptureError(ctx, errors.New("boom"), map[string]string{"code": "internal_error", "component": "httpapi"})
	r.Flush(2 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := body
		mu.Unlock()
		if strings.Contains(got, "trace-w51-aaa") {
			if strings.Contains(got, "super-secret-dsn") {
				t.Fatal("secret leaked")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := body
	mu.Unlock()
	if len(got) > 400 {
		got = got[:400]
	}
	t.Fatalf("missing trace id in event: %s", got)
}

func TestCaptureErrorRateLimited(t *testing.T) {
	var hits atomicCounter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN: "http://k@" + host + "/1", SampleRate: 1,
		Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{"code": "internal_error", "component": "storm"}
	for i := 0; i < 40; i++ {
		r.CaptureError(context.Background(), errors.New("x"), tags)
	}
	r.Flush(2 * time.Second)
	time.Sleep(200 * time.Millisecond)
	n := hits.Load()
	if n == 0 || n > 25 {
		t.Fatalf("hits=%d want ~1..20 envelopes", n)
	}
}

func TestCapturePanicOnce(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if len(b) > 0 {
			mu.Lock()
			count++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN: "http://k@" + host + "/1", SampleRate: 1,
		Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := logging.With(context.Background(), func(id *logging.IDs) { id.TraceID = "panic-trace" })
	r.CapturePanic(ctx, "kaboom", []byte("STACK"), map[string]string{"component": "jobs"})
	r.Flush(2 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := count
		mu.Unlock()
		if n >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("panic not reported")
}

type atomicCounter struct {
	n sync.Mutex
	v int
}

func (c *atomicCounter) Add(d int) {
	c.n.Lock()
	c.v += d
	c.n.Unlock()
}
func (c *atomicCounter) Load() int {
	c.n.Lock()
	defer c.n.Unlock()
	return c.v
}
