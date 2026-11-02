//go:build integration

package rules

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// TestIntegrationW73bListPages: the admin table searches, sorts and pages named lists on the server, the list
// view carries entryCount but no entries, and another organisation's lists never show.
func TestIntegrationW73bListPages(t *testing.T) {
	r, _ := w73Repo(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	a, b := asOrgW73(orgA, tenancy.RoleAdmin), asOrgW73(orgB, tenancy.RoleAdmin)
	for _, in := range []RuleListCreate{
		{Name: "vip-domains", Kind: "domains", Entries: []string{"partner.it", "studio.it", "court.it"}},
		{Name: "blocked", Kind: "addresses", Entries: []string{"spam@x.com"}},
		{Name: "vip-senders", Kind: "addresses", Entries: []string{"a@b.it", "c@d.it"}},
	} {
		if _, err := r.CreateList(a, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CreateList(b, RuleListCreate{Name: "vip-other", Kind: "domains"}); err != nil {
		t.Fatal(err)
	}

	page, err := r.ListListsPage(a, ListListsFilter{Q: "VIP", IncludeTotal: true, Sort: "-entryCount"})
	if err != nil || page.Total == nil || *page.Total != 2 || len(page.Data) != 2 {
		t.Fatalf("search = %+v %v", page, err)
	}
	if page.Data[0].Name != "vip-domains" || page.Data[0].EntryCount != 3 || page.Data[0].Entries != nil {
		t.Fatalf("sorted row without entries = %+v", page.Data[0])
	}
	second, err := r.ListListsPage(a, ListListsFilter{Limit: 1, Page: 2, Sort: "name", IncludeTotal: true})
	if err != nil || len(second.Data) != 1 || second.Data[0].Name != "vip-domains" || *second.Total != 3 {
		t.Fatalf("page 2 = %+v %v", second, err)
	}
	kind, err := r.ListListsPage(a, ListListsFilter{Kind: "addresses", Sort: "bogus"})
	if err != nil || len(kind.Data) != 2 || kind.Data[0].Name != "blocked" {
		t.Fatalf("kind filter with unknown sort = %+v %v", kind, err)
	}
	if regex, err := r.ListListsPage(a, ListListsFilter{Q: ".*"}); err != nil || len(regex.Data) != 0 {
		t.Fatalf("q is a literal, not a regex: %+v %v", regex, err)
	}
	other, err := r.ListListsPage(b, ListListsFilter{Q: "vip", IncludeTotal: true})
	if err != nil || *other.Total != 1 || other.Data[0].Name != "vip-other" {
		t.Fatalf("cross-tenant = %+v %v", other, err)
	}
}
