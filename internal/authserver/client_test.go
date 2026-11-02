package authserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSignAndVerify(t *testing.T) {
	t.Parallel()
	body := []byte(`{"accountId":"acc_1","protocol":"imap"}`)
	sig := signBody("sekret", 1700000000, body)
	if !VerifySignature("sekret", 1700000000, body, sig) {
		t.Fatal("verify failed")
	}
	if VerifySignature("sekret", 1700000000, body, "v1,deadbeef") {
		t.Fatal("tampered should fail")
	}
}

func TestFetchOKAndTamperReject(t *testing.T) {
	t.Parallel()
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-EB-Timestamp"), 10, 64)
		gotSig = r.Header.Get("X-EB-Signature")
		if !VerifySignature("sekret", ts, raw, gotSig) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Response{User: "u", Pass: "p"})
	}))
	defer srv.Close()

	r := &Resolver{HTTP: srv.Client(), now: func() time.Time { return time.Unix(1700000000, 0).UTC() }}
	creds, err := r.fetch(context.Background(), srv.URL, "sekret", time.Second, Request{AccountID: "acc_1", Protocol: "imap"})
	if err != nil || creds.User != "u" || creds.Pass != "p" {
		t.Fatalf("fetch: %v %#v", err, creds)
	}
	if gotSig == "" {
		t.Fatal("missing signature")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := r.fetch(context.Background(), bad.URL, "sekret", time.Second, Request{AccountID: "acc_1", Protocol: "imap"}); errs.CodeOf(err) != errs.CodeAuthServerFailed {
		t.Fatalf("want auth_server_failed, got %v", err)
	}
}

func TestCacheTTLAndNoPersist(t *testing.T) {
	t.Parallel()
	c := newMemCache()
	now := time.Unix(100, 0).UTC()
	c.put("k", Creds{User: "u", Pass: "secret"}, now.Add(time.Minute))
	if _, ok := c.get("k", now); !ok {
		t.Fatal("miss")
	}
	if _, ok := c.get("k", now.Add(2*time.Minute)); ok {
		t.Fatal("should expire")
	}
}

func TestBreakerOpens(t *testing.T) {
	t.Parallel()
	b := newBreaker()
	b.threshold = 2
	b.cooldown = time.Minute
	now := time.Unix(1, 0).UTC()
	b.record(now, false)
	if !b.allow(now) {
		t.Fatal("still closed")
	}
	b.record(now, false)
	if b.allow(now) {
		t.Fatal("should be open")
	}
	if !b.allow(now.Add(2 * time.Minute)) {
		t.Fatal("should cool down")
	}
}

func TestFetchTimeout(t *testing.T) {
	t.Parallel()
	var started atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started.Store(true)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), now: time.Now}
	_, err := r.fetch(context.Background(), srv.URL, "s", 20*time.Millisecond, Request{AccountID: "a", Protocol: "imap"})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !started.Load() {
		t.Fatal("server not hit")
	}
}
