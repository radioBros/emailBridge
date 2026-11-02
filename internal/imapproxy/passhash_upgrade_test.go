package imapproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func legacyV1(password string) string {
	salt := []byte("0123456789abcdef")
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	return "v1$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(sum[:])
}

// rehashStore is a credential store that supports the transparent upgrade.
type rehashStore struct {
	creds   map[string]*Credential
	rehashN int
}

func (r *rehashStore) Get(_ context.Context, id string) (*Credential, error) { return r.creds[id], nil }

func (r *rehashStore) Rehash(_ context.Context, id, oldHash, newHash string) error {
	c := r.creds[id]
	if c == nil || c.Hash != oldHash {
		return nil
	}
	r.rehashN++
	c.Hash = newHash
	return nil
}

func TestLegacyHashUpgradedOnLogin(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	legacy := legacyV1("legacy-password-1")
	st := &rehashStore{creds: map[string]*Credential{accID: {ID: accID, OrganizationID: org, Hash: legacy}}}
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, State: "connected"}}},
		Creds:    st,
	}
	ctx := context.Background()
	// Wrong password: no upgrade.
	if _, err := a.Authenticate(ctx, accID, "not-the-password", "1.2.3.4:5"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatal(err)
	}
	if st.creds[accID].Hash != legacy {
		t.Fatal("failed login must not rehash")
	}
	got, err := a.Authenticate(ctx, accID, "legacy-password-1", "1.2.3.4:5")
	if err != nil || got.ID != accID {
		t.Fatalf("legacy login: %v", err)
	}
	if !strings.HasPrefix(st.creds[accID].Hash, "$argon2id$") || st.rehashN != 1 {
		t.Fatalf("not upgraded: %s", st.creds[accID].Hash)
	}
	// Second login verifies the argon2id hash and does not rehash again.
	if _, err := a.Authenticate(ctx, accID, "legacy-password-1", "1.2.3.4:5"); err != nil {
		t.Fatal(err)
	}
	if st.rehashN != 1 {
		t.Fatal("current hash must not be rehashed")
	}
	if _, err := a.Authenticate(ctx, accID, "legacy-password-2", "1.2.3.4:5"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatal(err)
	}
}

func TestNewPasswordsAreArgon2id(t *testing.T) {
	h, err := hashPassword("brand-new-password")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("%s %v", h, err)
	}
	if !verifyPassword(h, "brand-new-password") || verifyPassword(h, "brand-new-passwore") {
		t.Fatal("verify")
	}
	if !verifyPassword(legacyV1("old"), "old") {
		t.Fatal("legacy still verifies")
	}
	// Store without Rehash support: login works, no upgrade, no panic.
	if upgradeHash(context.Background(), memCredStore{}, "acc", legacyV1("old"), "old") {
		t.Fatal("no rehasher")
	}
	if upgradeHash(context.Background(), &rehashStore{}, "acc", h, "brand-new-password") {
		t.Fatal("current hash needs no upgrade")
	}
}

func TestUnknownAccountAndMissingCredentialFail(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, State: "connected"}}},
		Creds:    &rehashStore{creds: map[string]*Credential{}},
	}
	ctx := context.Background()
	if _, err := a.Authenticate(ctx, accID, "some-password", "1.2.3.4:5"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(ctx, store.NewID(store.PrefixAccount), "some-password", "1.2.3.4:5"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatal(err)
	}
}
