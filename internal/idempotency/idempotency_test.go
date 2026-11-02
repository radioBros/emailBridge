package idempotency

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestValidKeyAndExempt(t *testing.T) {
	for key, want := range map[string]bool{
		"": false, "a": true, strings.Repeat("a", 255): true, strings.Repeat("a", 256): false,
		"with space~": true, "tab\t": false, "é": false,
	} {
		if ValidKey(key) != want {
			t.Errorf("ValidKey(%q) != %v", key, want)
		}
	}
	for path, want := range map[string]bool{
		"/v1/auth/sign-in/email": true, "/v1/accounts/acc_1/outbound-messages": true,
		"/v1/accounts": false, "/v1/outbound-messages/out_1": false,
	} {
		if Exempt(path) != want {
			t.Errorf("Exempt(%q) != %v", path, want)
		}
	}
}

func TestHash(t *testing.T) {
	if Hash("", []byte(`{"a":1,"b":[1,2]}`)) != Hash("", []byte("{ \"b\": [1,2],\n \"a\": 1 }")) {
		t.Error("JSON key order and whitespace must not matter")
	}
	if Hash("", []byte(`{"a":1}`)) == Hash("x=1", []byte(`{"a":1}`)) {
		t.Error("the query must matter")
	}
	if Hash("", []byte("not json")) == Hash("", []byte("not  json")) {
		t.Error("non-JSON bodies are compared byte for byte")
	}
	if Hash("", nil) != Hash("", []byte{}) {
		t.Error("empty bodies are equal")
	}
	if Hash("", []byte(`{"n":1.0}`)) == Hash("", []byte(`{"n":1}`)) {
		t.Error("numbers keep their literal form")
	}
}

func TestDefaults(t *testing.T) {
	m := &Middleware{}
	if m.ttl() != 24*time.Hour || m.stale() != 4*time.Minute || m.log() == nil || m.now().IsZero() {
		t.Fatal("defaults")
	}
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	m = &Middleware{TTL: time.Hour, Stale: time.Second, Log: slog.Default(), Now: func() time.Time { return fixed }}
	if m.ttl() != time.Hour || m.stale() != time.Second || m.now() != fixed.UTC() {
		t.Fatal("overrides")
	}
}

func TestWriteReplayAndRecorder(t *testing.T) {
	w := httptest.NewRecorder()
	writeReplay(w, &record{Response: `{"ok":true}`, Headers: map[string]string{"Location": "/x"}})
	if w.Code != http.StatusOK || w.Header().Get(ReplayedHeader) != "true" || w.Header().Get("Location") != "/x" || w.Body.String() != `{"ok":true}` {
		t.Fatalf("%d %v %q", w.Code, w.Header(), w.Body.String())
	}

	inner := httptest.NewRecorder()
	rec := &recorder{ResponseWriter: inner, status: http.StatusOK}
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusTeapot) // only the first status counts
	_, _ = rec.Write([]byte("hello"))
	if rec.status != http.StatusCreated || rec.body.String() != "hello" || rec.Unwrap() != inner {
		t.Fatalf("recorder: %d %q", rec.status, rec.body.String())
	}
}

func TestModuleStartNeedsDependencies(t *testing.T) {
	m := &Module{}
	if m.Name() != "idempotency" || len(m.Roles()) != 1 || m.Stop(context.Background()) != nil {
		t.Fatal("module metadata")
	}
	if err := m.Start(context.Background(), app.New(app.Options{Log: slog.Default()})); err == nil {
		t.Fatal("Start without a database must fail")
	}
}
