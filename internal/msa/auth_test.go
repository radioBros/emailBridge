package msa

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
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

type memKeys struct {
	caller *tenancy.Caller
	err    error
}

func (m *memKeys) VerifySecret(context.Context, string) (*tenancy.Caller, error) {
	return m.caller, m.err
}

type memCredStore map[string]*Credential

func (m memCredStore) Get(_ context.Context, accountID string) (*Credential, error) {
	return m[accountID], nil
}

func TestAuthenticatorPassword(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, err := hashPassword("submission-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	a := &Authenticator{
		Accounts: &memAccounts{
			byID:    map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, Email: "a@ex.com", State: "connected"}},
			byEmail: map[string]*AccountRef{"a@ex.com": {ID: accID, OrganizationID: org, Email: "a@ex.com", State: "connected"}},
		},
		Creds: memCredStore{accID: {ID: accID, OrganizationID: org, Hash: hash}},
	}
	got, err := a.Authenticate(context.Background(), accID, "submission-pass-1", "1.2.3.4:9")
	if err != nil || got.Account.ID != accID || got.Caller.Kind != tenancy.CallerSystem {
		t.Fatalf("got %+v err %v", got, err)
	}
	got, err = a.Authenticate(context.Background(), "a@ex.com", "submission-pass-1", "1.2.3.4:9")
	if err != nil || got.Account.ID != accID {
		t.Fatalf("email auth: %+v %v", got, err)
	}
	if _, err := a.Authenticate(context.Background(), accID, "nope", "1.2.3.4:9"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatalf("bad pass: %v", err)
	}
}

func TestAuthenticatorAPIKey(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	a := &Authenticator{
		Accounts: &memAccounts{
			byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, State: "connected"}},
		},
		Creds: memCredStore{},
		APIKeys: &memKeys{caller: &tenancy.Caller{
			Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: org,
			AccountID: accID, Scopes: []string{"outbound:write"},
		}},
	}
	got, err := a.Authenticate(context.Background(), accID, auth.APIKeyPrefix+"secrettoken", "10.0.0.1:1")
	if err != nil || got.Caller.APIKeyID != "key_1" {
		t.Fatalf("%+v %v", got, err)
	}
	a.APIKeys = &memKeys{caller: &tenancy.Caller{
		Kind: tenancy.CallerAPIKey, OrganizationID: org, AccountID: "", Scopes: []string{"outbound:write"},
	}}
	if _, err := a.Authenticate(context.Background(), accID, auth.APIKeyPrefix+"x", "10.0.0.1:1"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatalf("org-wide key: %v", err)
	}
}

func TestAuthenticatorDisabled(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("submission-pass-1")
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{
			accID: {ID: accID, OrganizationID: org, State: "disabled"},
		}},
		Creds: memCredStore{accID: {Hash: hash}},
	}
	if _, err := a.Authenticate(context.Background(), accID, "submission-pass-1", "1.1.1.1:1"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatalf("disabled: %v", err)
	}
}

func TestAuthenticatorRateLimit(t *testing.T) {
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{}},
		Creds:    memCredStore{},
		AuthRate: newLimiter(1),
	}
	_, _ = a.Authenticate(context.Background(), "x", "y", "9.9.9.9:1")
	_, err := a.Authenticate(context.Background(), "x", "y", "9.9.9.9:1")
	if errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("want rate limited, got %v", err)
	}
}
