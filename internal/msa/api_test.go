package msa

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestAPIHandlers(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &memRepo{data: map[string]*Credential{}}, OwnsAccount: ownsAll}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	out, err := svc.getPassword(ctx, &AccountPath{AccountID: accID})
	if err != nil || out.Body.Set {
		t.Fatalf("%+v %v", out, err)
	}
	out, err = svc.putPassword(ctx, &putIn{AccountPath: AccountPath{AccountID: accID}, Body: struct {
		Password string `json:"password"`
	}{Password: "long-enough-password"}})
	if err != nil || !out.Body.Set {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := svc.deletePassword(ctx, &AccountPath{AccountID: accID}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIRegister(t *testing.T) {
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Repo: &memRepo{}, OwnsAccount: ownsAll}
	svc.Register(srv)
}

func TestLoginServer(t *testing.T) {
	var user, pass string
	ls := &loginServer{auth: func(u, p string) error { user, pass = u, p; return nil }}
	ch, done, err := ls.Next(nil)
	if err != nil || done || string(ch) != "Username:" {
		t.Fatalf("%s %v %v", ch, done, err)
	}
	ch, done, err = ls.Next([]byte("alice"))
	if err != nil || done || string(ch) != "Password:" {
		t.Fatalf("%s %v %v", ch, done, err)
	}
	_, done, err = ls.Next([]byte("secret"))
	if err != nil || !done || user != "alice" || pass != "secret" {
		t.Fatalf("%v %v %q %q", done, err, user, pass)
	}
	ls2 := &loginServer{auth: func(u, p string) error { user, pass = u, p; return nil }}
	_, done, err = ls2.Next([]byte("bob"))
	if err != nil || done {
		t.Fatal(err)
	}
	_, done, err = ls2.Next([]byte("pw"))
	if err != nil || !done || user != "bob" {
		t.Fatalf("%v %q", err, user)
	}
}

func TestModuleMeta(t *testing.T) {
	m := &Module{}
	if m.Name() != "msa" || len(m.Roles()) != 1 {
		t.Fatal(m.Name(), m.Roles())
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Service() != nil {
		t.Fatal()
	}
}
