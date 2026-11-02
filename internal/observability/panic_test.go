package observability_test

import (
	"context"
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

func TestGoRecoversAndReports(t *testing.T) {
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
	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://testkey@" + host + "/1"

	sys := logging.New(logging.Options{Node: "n1", Role: "api", Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(context.Background()) }()
	log := sys.For(logging.CatCrash, "panic-test")

	r, err := observability.NewReporter(observability.ReporterOptions{
		DSN: dsn, SampleRate: 1, NodeID: "n1", Role: "api",
		Reportable: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	observability.Go(context.Background(), "panic-test", log, r, func(context.Context) {
		defer close(done)
		panic("kaboom")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not finish")
	}
	r.Flush(2 * time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(body)
		mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("panic was not reported to sentry")
}

func TestRecoverDefer(t *testing.T) {
	sys := logging.New(logging.Options{Stdout: io.Discard, Fallback: io.Discard})
	defer func() { _ = sys.Close(context.Background()) }()
	log := sys.For(logging.CatCrash, "defer-test")
	r, _ := observability.NewReporter(observability.ReporterOptions{})
	func() {
		defer observability.Recover(context.Background(), "defer-test", log, r)
		panic("x")
	}()
}
