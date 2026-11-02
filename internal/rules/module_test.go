package rules

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func TestModuleWebhookOnlyProvidesEngine(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleWebhook: true}, NodeID: "n", Log: quiet})
	m := &Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Engine() == nil || m.Repo() == nil {
		t.Fatal("engine/repo")
	}
	if eng, err := app.MustGet[*Engine](a); err != nil || eng == nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModuleAPIRegisters(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Roles()[0] != app.RoleAPI {
		t.Fatal(m.Roles())
	}
}
