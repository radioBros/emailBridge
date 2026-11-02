//go:build integration

package suppression

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationPaginationAndSwitchOff(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = EnsureIndexes(ctx, db)
	org := store.NewID(store.PrefixOrganization)
	cctx := orgCtx(org)
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}

	var first *List
	for i := 0; i < 3; i++ {
		l, err := svc.CreateList(cctx, CreateListInput{Name: fmt.Sprintf("L%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = l
		}
		for j := 0; j < 3; j++ {
			_, _, err := svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: fmt.Sprintf("u%d%d@ex.test", i, j)})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	page1, err := svc.ListLists(cctx, ListFilter{Limit: 2, Sort: "name", IncludeTotal: true})
	if err != nil || len(page1.Data) != 2 || page1.NextCursor == nil {
		t.Fatalf("%+v %v", page1, err)
	}
	page2, err := svc.ListLists(cctx, ListFilter{Limit: 2, Cursor: *page1.NextCursor, Sort: "name"})
	if err != nil || len(page2.Data) < 1 {
		t.Fatalf("%+v %v", page2, err)
	}
	ep1, err := svc.ListEntries(cctx, EntryFilter{ListID: first.ID, Limit: 2, IncludeTotal: true})
	if err != nil || len(ep1.Data) != 2 || ep1.NextCursor == nil {
		t.Fatalf("%+v %v", ep1, err)
	}
	ep2, err := svc.ListEntries(cctx, EntryFilter{ListID: first.ID, Limit: 2, Cursor: *ep1.NextCursor})
	if err != nil || len(ep2.Data) < 1 {
		t.Fatalf("%+v %v", ep2, err)
	}

	// version mismatch
	if _, err := svc.PatchList(cctx, first.ID, `"v999"`, PatchListInput{}); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if err := svc.DeleteList(cctx, first.ID, `"v999"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if _, err := svc.GetList(cctx, store.NewID(store.PrefixSuppression)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.GetList(cctx, "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, _, err := svc.AddEntry(cctx, first.ID, CreateEntryInput{Address: "bad"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := svc.DeleteEntry(cctx, first.ID, "missing"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	// turn suppression off via settings doc
	_, _ = db.Collection(settings.SettingsCollection).InsertOne(ctx, settings.Doc{
		ID: settings.OrgDocID(org),
		Switches: map[string]settings.SwitchValue{
			"sending.suppression": {Value: false},
		},
	})
	if svc.SuppressionEnabled(cctx, org) {
		t.Fatal("should be off")
	}
	if err := svc.CheckAddresses(cctx, org, []string{"u00@ex.test"}, ""); err != nil {
		t.Fatal(err)
	}

	_, _ = svc.ListLists(cctx, ListFilter{Limit: 1, Page: 2, Sort: "id"})
	_, _ = svc.ListLists(cctx, ListFilter{Limit: 1, Page: 2, Sort: "-id"})
	_, _ = svc.ListEntries(cctx, EntryFilter{ListID: first.ID, Limit: 1, Page: 2})

	svc.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	if err := svc.AddUnsubscribe(ctx, org, "again@ex.test", "x", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddUnsubscribe(ctx, org, "again@ex.test", "x", ""); err != nil {
		t.Fatal(err)
	}
	longReason := strings.Repeat("r", 300)
	if err := svc.AddBounce(ctx, org, "long@ex.test", longReason); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDesc(strings.Repeat("d", 600)); err == nil {
		t.Fatal("desc")
	}
	if _, _, err := svc.AddEntry(cctx, first.ID, CreateEntryInput{Address: "ok@ex.test", Reason: strings.Repeat("r", 300)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	_ = (&Service{Repo: &Repo{DB: db}}).registry()
}
