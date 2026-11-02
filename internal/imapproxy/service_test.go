package imapproxy

import (
	"context"
	"errors"
	"strings"
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
	if err != nil || !st.Set || st.UpdatedAt == nil {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = svc.Status(ctx, accID)
	if err != nil || !st.Set || st.UpdatedAt == nil {
		t.Fatalf("%+v %v", st, err)
	}
	viewer := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_v", OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if err := svc.ClearPassword(viewer, accID); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("%v", err)
	}
	if err := svc.ClearPassword(ctx, accID); err != nil {
		t.Fatal(err)
	}
}

func TestSetPasswordNoTenant(t *testing.T) {
	svc := &Service{Repo: &memRepo{}, OwnsAccount: ownsAll}
	accID := store.NewID(store.PrefixAccount)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", Role: tenancy.RoleAdmin,
	})
	_, err := svc.SetPassword(ctx, accID, "long-enough-password")
	if !errors.Is(err, store.ErrNoTenant) {
		t.Fatalf("%v", err)
	}
}

func TestQuoteEmptyAndBuffered(t *testing.T) {
	if quoteIMAP("") != `""` {
		t.Fatal(quoteIMAP(""))
	}
	r := newLineReader(strings.NewReader(""))
	if b := r.buffered(); len(b) != 0 {
		t.Fatal(b)
	}
}

func TestMigrationAndGate(t *testing.T) {
	m := Migration(30)
	if m.Number != 30 || m.Up == nil {
		t.Fatalf("%+v", m)
	}
	mod := &Module{Env: "production"}
	if mod.gate().IMAPProxyEnabled(context.Background()) {
		t.Fatal("default off")
	}
}

func TestServerDisabled(t *testing.T) {
	s := NewServer(Options{Enabled: false}, &connHandler{}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestReadAstring(t *testing.T) {
	v, rest, ok := readAstring(`"hi\"x" more`)
	if !ok || v != `hi"x` || stringsTrim(rest) != "more" {
		t.Fatalf("%q %q", v, rest)
	}
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
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
