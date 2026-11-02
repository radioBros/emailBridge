package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "emailbridge "+version) {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "serve") {
		t.Fatalf("help: code=%d out=%q", code, out.String())
	}
	errb.Reset()
	if code := run([]string{"nope"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("unknown: code=%d err=%q", code, errb.String())
	}
}

func TestKeysGenerate(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"keys", "generate", "k1"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "k1:") {
		t.Fatalf("keys generate: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"keys", "generate"}, &out, &errb); code != 0 || len(out.String()) < 40 {
		t.Fatalf("default kid: %d %q", code, out.String())
	}
	if code := run([]string{"keys"}, &out, &errb); code != 2 {
		t.Fatal("usage")
	}
	if code := run([]string{"keys", "generate", "bad:kid"}, &out, &errb); code != 1 {
		t.Fatal("bad kid")
	}
}

// minimalEnv sets the required configuration with values that pass validation (stores unreachable).
func minimalEnv(t *testing.T) {
	t.Helper()
	b := func(n int) string {
		k := make([]byte, n)
		_, _ = rand.Read(k)
		return base64.StdEncoding.EncodeToString(k)
	}
	t.Setenv("EB_MONGO_URI", "mongodb://127.0.0.1:1/?replicaSet=rs0")
	t.Setenv("EB_S3_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("EB_AUTH_SECRET", b(32))
	t.Setenv("EB_KEYS_LOCAL", "k1:"+b(32))
	t.Setenv("EB_PUBLIC_URL", "https://mail.example.test")
	t.Setenv("EB_DATA_DIR", t.TempDir())
}

func TestServeRejectsBadRoles(t *testing.T) {
	minimalEnv(t)
	t.Setenv("EB_ROLES", "mailer")
	var out, errb bytes.Buffer
	if code := run([]string{"serve"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "unknown role") {
		t.Fatalf("serve: code=%d err=%q", code, errb.String())
	}
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("EB_HTTP_ADDR", ":"+port)
	if err := healthcheck(nil); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(nil); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("unhealthy: %v", err)
	}
	t.Setenv("EB_HTTP_ADDR", "bad")
	if err := healthcheck(nil); err == nil {
		t.Fatal("bad address must fail")
	}
}
