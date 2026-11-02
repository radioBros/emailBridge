//go:build integration

package imapproxy

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationRehashUpgradesLegacy(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := &Repo{DB: db}
	acc := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	legacy := legacyV1("long-enough-password")
	if err := r.Upsert(ctx, org, acc, legacy, "usr_1"); err != nil {
		t.Fatal(err)
	}
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{acc: {ID: acc, OrganizationID: org, State: "connected"}}},
		Creds:    r,
	}
	if _, err := a.Authenticate(ctx, acc, "long-enough-password", "1.2.3.4:5"); err != nil {
		t.Fatal(err)
	}
	c, err := r.Get(ctx, acc)
	if err != nil || c == nil || !strings.HasPrefix(c.Hash, "$argon2id$") || c.OrganizationID != org {
		t.Fatalf("not upgraded: %+v %v", c, err)
	}
	if _, err := a.Authenticate(ctx, acc, "long-enough-password", "1.2.3.4:5"); err != nil {
		t.Fatalf("argon2id login: %v", err)
	}
	// Compare-and-set: a stale old hash never overwrites a newer password.
	if err := r.Rehash(ctx, acc, legacy, "v1$00$00"); err != nil {
		t.Fatal(err)
	}
	c2, _ := r.Get(ctx, acc)
	if c2.Hash != c.Hash {
		t.Fatal("stale rehash overwrote the stored hash")
	}
}
