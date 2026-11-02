package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestHealthListenerOnlyOnNonAPINodes(t *testing.T) {
	ctx := context.Background()
	h := &HealthListener{Addr: "127.0.0.1:0"}
	syncNode := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := h.Start(ctx, syncNode); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Stop(ctx) }()
	base := "http://" + h.ln.Addr().String()
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusOK, "/v1/me": http.StatusNotFound, "/": http.StatusNotFound} {
		resp, err := http.Get(base + path) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}

	api := &HealthListener{Addr: "127.0.0.1:0"}
	apiNode := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := api.Start(ctx, apiNode); err != nil || api.ln != nil || api.Stop(ctx) != nil {
		t.Fatalf("the api node serves health itself: %v", err)
	}
	if api.Name() == "" || api.Roles() != nil {
		t.Fatal("metadata")
	}
}
