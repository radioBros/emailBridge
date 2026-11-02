//go:build integration

package tenancy

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
)

// The organisations list shows member and account counts per row and filters by name or slug.
// Accounts being deleted do not count (same rule as the account quota).
func TestIntegrationOrganizationListCountsAndSearch(t *testing.T) {
	svc, repo, db, _ := orgHarness(t)
	ic := ctxAs("", "root", "", true)

	acme, err := svc.CreateOrganization(ic, "Acme Legal", "acme-legal", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := repo.CreateOrganization(ic, "Beta", "beta")
	if err != nil {
		t.Fatal(err)
	}

	insert := func(coll string, doc bson.M) {
		t.Helper()
		if _, err := db.Collection(coll).InsertOne(ic, doc); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		insert(MembershipsCollection, bson.M{"_id": store.NewID(store.PrefixMembership), "organizationId": acme.ID})
	}
	insert(MembershipsCollection, bson.M{"_id": store.NewID(store.PrefixMembership), "organizationId": beta.ID})
	insert("accounts", bson.M{"_id": store.NewID(store.PrefixAccount), "organizationId": acme.ID, "state": "connected"})
	insert("accounts", bson.M{"_id": store.NewID(store.PrefixAccount), "organizationId": acme.ID, "state": "new"})
	insert("accounts", bson.M{"_id": store.NewID(store.PrefixAccount), "organizationId": acme.ID, "state": "deleting"})

	all, err := svc.ListOrganizations(ic, ListFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string][2]int64{}
	for _, o := range all.Data {
		counts[o.ID] = [2]int64{o.MemberCount, o.AccountCount}
	}
	if got := counts[acme.ID]; got != [2]int64{3, 2} {
		t.Fatalf("acme members/accounts = %v, want [3 2] (the deleting account must not count)", got)
	}
	if got := counts[beta.ID]; got != [2]int64{1, 0} {
		t.Fatalf("beta members/accounts = %v, want [1 0]", got)
	}

	found, err := svc.ListOrganizations(ic, ListFilter{Limit: 50, Q: "ACME"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Data) != 1 || found.Data[0].ID != acme.ID {
		t.Fatalf("search ACME: got %d rows", len(found.Data))
	}
	// Regex metacharacters are matched literally, never interpreted.
	none, err := svc.ListOrganizations(ic, ListFilter{Limit: 50, Q: ".*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Data) != 0 {
		t.Fatalf("search .* must match literally, got %d rows", len(none.Data))
	}
}
