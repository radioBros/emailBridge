package imapproxy

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type memAccounts struct {
	byID    map[string]*AccountRef
	byEmail map[string]*AccountRef
}

func (m *memAccounts) ByID(_ context.Context, id string) (*AccountRef, error) {
	return m.byID[id], nil
}
func (m *memAccounts) ByEmail(_ context.Context, email string) (*AccountRef, error) {
	return m.byEmail[email], nil
}

type memCredStore map[string]*Credential

func (m memCredStore) Get(_ context.Context, accountID string) (*Credential, error) {
	return m[accountID], nil
}

func TestAuthenticator(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("proxy-password-1")
	a := &Authenticator{
		Accounts: &memAccounts{
			byID:    map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, Email: "u@ex.com", State: "connected"}},
			byEmail: map[string]*AccountRef{"u@ex.com": {ID: accID, OrganizationID: org, Email: "u@ex.com", State: "connected"}},
		},
		Creds: memCredStore{accID: {Hash: hash}},
	}
	got, err := a.Authenticate(context.Background(), "u@ex.com", "proxy-password-1", "1.2.3.4:5")
	if err != nil || got.ID != accID {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := a.Authenticate(context.Background(), accID, "wrong-password!", "1.2.3.4:5"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatalf("%v", err)
	}
	a.AuthRate = newLimiter(1)
	_, _ = a.Authenticate(context.Background(), "x", "y", "8.8.8.8:1")
	if _, err := a.Authenticate(context.Background(), "x", "y", "8.8.8.8:1"); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("%v", err)
	}
}
