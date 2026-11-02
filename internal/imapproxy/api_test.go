package imapproxy

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
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	svc.Register(srv)
}

func TestModuleMeta(t *testing.T) {
	m := &Module{}
	if m.Name() != "imapproxy" || len(m.Roles()) != 1 {
		t.Fatal()
	}
	_ = m.Stop(context.Background())
	if m.Service() != nil {
		t.Fatal()
	}
}
