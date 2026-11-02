package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestModuleStart(t *testing.T) {
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, &store.DB{})
	app.Provide(a, testCipher(t))
	app.Provide(a, srv)
	m := &Module{Env: "test", Registry: features.DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("nil svc")
	}
}

func TestProviderErrorBranches(t *testing.T) {
	p := &OpenAICompat{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}
	if _, err := p.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("want err")
	}
	p2 := &OpenAICompat{BaseURL: ""}
	_ = p2.client()
	a := &Anthropic{HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	if _, err := a.Complete(context.Background(), "k", "m", "s", "u"); err == nil {
		t.Fatal("want err")
	}
}
