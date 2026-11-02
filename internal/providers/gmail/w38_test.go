package gmail

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func TestPush_productionRequiresServiceAccount(t *testing.T) {
	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := &PushHandler{
		RequirePushSA: true,
		Verify:        StaticVerifier{Token: "tok"},
		Log:           log,
	}
	req := httptest.NewRequest(http.MethodPost, "/callbacks/gmail", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
	if !strings.Contains(logged.String(), "EB_GMAIL_PUSH_SERVICE_ACCOUNT") {
		t.Fatalf("log %q", logged.String())
	}
}

func TestPush_productionWithSAContinues(t *testing.T) {
	h := &PushHandler{
		RequirePushSA:      true,
		PushServiceAccount: "push@sa.iam.gserviceaccount.com",
		Verify:             StaticVerifier{Token: "tok"},
	}
	req := httptest.NewRequest(http.MethodPost, "/callbacks/gmail", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("should not refuse when SA set")
	}
}

func TestParseServiceAccountKey_type(t *testing.T) {
	if _, err := ParseServiceAccountKey([]byte(`{"type":"user","client_email":"a","private_key":"k"}`)); err == nil {
		t.Fatal("expected type error")
	}
	k, err := ParseServiceAccountKey([]byte(`{"type":"service_account","client_email":"a@b","private_key":"k"}`))
	if err != nil || k.Type != "service_account" {
		t.Fatal(err, k)
	}
	src, err := NewServiceAccountSourceFromJSON([]byte(`{"client_email":"a@b","private_key":"k"}`), "u@ex.com", nil)
	if err != nil || src.Subject != "u@ex.com" {
		t.Fatal(err, src)
	}
	if _, err := NewServiceAccountSourceFromJSON([]byte(`{}`), "u", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestModuleStart_productionRequirePushSA(t *testing.T) {
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Log: slog.Default(), Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1"})
	app.Provide(a, srv)
	m := &Module{CallbackURL: "http://cb.example", Env: "production", Log: slog.Default()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	h := m.Push()
	if h == nil || !h.RequirePushSA || h.PushServiceAccount != "" {
		t.Fatalf("%+v", h)
	}
	_ = m.Stop(context.Background())
}
