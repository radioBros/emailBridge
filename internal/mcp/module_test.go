package mcp

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func TestModuleStartUnit(t *testing.T) {
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, srv)
	m := &Module{Env: "test", Registry: features.DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Handler() == nil {
		t.Fatal("handler")
	}
	// non-api role skips
	m2 := &Module{Env: "test"}
	a2 := app.New(app.Options{Roles: app.RoleSet{}})
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
}
