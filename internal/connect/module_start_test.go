package connect

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestModuleStartAPI(t *testing.T) {
	db := testMongo(t)
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1"})
	app.Provide(a, db)
	app.Provide(a, srv)
	app.Provide(a, autoconfig.NewService(nil))
	m := &Module{
		PublicURL: "https://gw.example.com",
		Env:       "test",
		Enabled:   func(context.Context, string) bool { return true },
		Create: func(context.Context, string, string, string, string, *autoconfig.Server, *autoconfig.Server) (string, error) {
			return "acc_x", nil
		},
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	_ = m.Stop(context.Background())

	m2 := &Module{}
	a2 := app.New(app.Options{Roles: app.RoleSet{}, NodeID: "n2"})
	app.Provide(a2, db)
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
}

func TestExpandUserDefault(t *testing.T) {
	if expandUser("literal", "a@b.c") != "literal" {
		t.Fatal()
	}
	if expandUser("%EMAILLOCALPART%", "nolocal") != "nolocal" {
		t.Fatal()
	}
}

func TestPasswordDiscoverFallback(t *testing.T) {
	st := newMemStore()
	token, _ := putLink(st, "")
	s := NewService(st, autoconfig.NewService(nil))
	s.Enabled = func(context.Context, string) bool { return true }
	s.Create = func(context.Context, string, string, string, string, *autoconfig.Server, *autoconfig.Server) (string, error) {
		return store.NewID(store.PrefixAccount), nil
	}
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "user@gmail.com", Password: "pw", CSRF: boot.CSRF,
	})
	if err != nil || done.AccountID == "" {
		t.Fatalf("%+v %v", done, err)
	}
}
