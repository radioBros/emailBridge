//go:build integration

package suppression

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationListScopedUnsubscribe(t *testing.T) {
	db := testDB(t)
	ctx := orgCtx(store.NewID(store.PrefixOrganization))
	org, _ := store.OrganizationFrom(ctx)
	_ = EnsureIndexes(ctx, db)
	_ = EnsureMailingListIndex(ctx, db)
	svc := &Service{Repo: &Repo{DB: db}, Env: "test"}

	if err := svc.AddUnsubscribe(ctx, org, "user@example.test", "list-a", "news.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckAddresses(ctx, org, []string{"user@example.test"}, "other.example.com"); err != nil {
		t.Fatalf("other list should not suppress: %v", err)
	}
	if err := svc.CheckAddresses(ctx, org, []string{"user@example.test"}, ""); err != nil {
		t.Fatalf("transactional should not suppress list-scoped: %v", err)
	}
	if err := svc.CheckAddresses(ctx, org, []string{"user@example.test"}, "news.example.com"); errs.CodeOf(err) != errs.CodeRecipientSuppressed {
		t.Fatalf("same list should suppress: %v", err)
	}
	if err := svc.AddUnsubscribe(ctx, org, "user@example.test", "global", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckAddresses(ctx, org, []string{"user@example.test"}, "other.example.com"); errs.CodeOf(err) != errs.CodeRecipientSuppressed {
		t.Fatalf("global should suppress everything: %v", err)
	}
}
