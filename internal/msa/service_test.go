package msa

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memRepo struct {
	mu   sync.Mutex
	data map[string]*Credential
}

func (m *memRepo) Get(_ context.Context, accountID string) (*Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[accountID], nil
}
func (m *memRepo) Upsert(_ context.Context, orgID, accountID, hash, by string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]*Credential{}
	}
	m.data[accountID] = &Credential{ID: accountID, OrganizationID: orgID, Hash: hash, UpdatedAt: time.Now().UTC(), UpdatedBy: by}
	return nil
}
func (m *memRepo) Delete(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, accountID)
	return nil
}

func TestServicePasswordCRUD(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &memRepo{data: map[string]*Credential{}}, OwnsAccount: ownsAll}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	st, err := svc.Status(ctx, accID)
	if err != nil || st.Set {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := svc.SetPassword(ctx, accID, "short"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	st, err = svc.SetPassword(ctx, accID, "long-enough-password")
	if err != nil || !st.Set {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = svc.Status(ctx, accID)
	if err != nil || !st.Set {
		t.Fatalf("%+v %v", st, err)
	}
	if err := svc.ClearPassword(ctx, accID); err != nil {
		t.Fatal(err)
	}
	st, err = svc.Status(ctx, accID)
	if err != nil || st.Set {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestServiceForbidden(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &memRepo{}, OwnsAccount: ownsAll}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if _, err := svc.SetPassword(ctx, accID, "long-enough-password"); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

func TestServerStartDisabled(t *testing.T) {
	s := NewServer(Options{Enabled: false}, &Backend{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestServerStartListen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	be := &Backend{Gate: alwaysOn{}, Auth: &Authenticator{Accounts: &memAccounts{}, Creds: memCredStore{}}}
	s := NewServer(Options{
		Enabled: true, Addr: addr, AllowInsecureAuth: true, Domain: "t",
	}, be, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestMigrationShape(t *testing.T) {
	m := Migration(30)
	if m.Number != 30 || m.Name == "" || m.Up == nil {
		t.Fatalf("%+v", m)
	}
}

func TestFeatureGateDefaultOff(t *testing.T) {
	mod := &Module{Env: "production"}
	if mod.gate().MSAEnabled(context.Background()) {
		t.Fatal("default off")
	}
}

func ownsAll(context.Context, string, string) (bool, error) { return true, nil }

// Credentials are keyed by account ID: an account of another organisation is refused like a missing one, for
// status, set and clear (cross-tenant password set = sending / reading as that account).
func TestForeignAccountRefused(t *testing.T) {
	svc := &Service{Repo: &memRepo{data: map[string]*Credential{}}, OwnsAccount: func(context.Context, string, string) (bool, error) {
		return false, nil
	}}
	ctx := ownerCtxForTest()
	acc := store.NewID(store.PrefixAccount)
	if _, err := svc.Status(ctx, acc); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("status: %v", err)
	}
	if _, err := svc.SetPassword(ctx, acc, "a-long-enough-password"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("set: %v", err)
	}
	if err := svc.ClearPassword(ctx, acc); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("clear: %v", err)
	}
	if (&Service{Repo: &memRepo{}}).requireOwnAccount(ctx, acc) == nil {
		t.Fatal("a service without an ownership check must refuse")
	}
}

func ownerCtxForTest() context.Context {
	org := store.NewID(store.PrefixOrganization)
	return tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
}
