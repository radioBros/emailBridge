//go:build integration

package rules

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func w73Repo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db := testDB(t)
	if err := W73Migration(45).Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stats := NewStatsRecorder(db)
	stats.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Repo{DB: db, Engine: eng, Lists: &ListCache{DB: db}, Stats: stats}, db
}

func asOrgW73(org string, role tenancy.Role) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_w73", OrganizationID: org, Role: role,
	})
}

func TestIntegrationW73NamedListsCrossTenant(t *testing.T) {
	r, _ := w73Repo(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	a, b := asOrgW73(orgA, tenancy.RoleAdmin), asOrgW73(orgB, tenancy.RoleAdmin)

	la, err := r.CreateList(a, RuleListCreate{Name: "vip", Kind: "domains", Entries: []string{"Partner.com", "partner.com", " ", "acme.it"}})
	if err != nil || la.EntryCount != 2 || la.Version != 1 {
		t.Fatalf("create A: %+v %v", la, err)
	}
	// The same name is free in another organisation; taken in the same one.
	lb, err := r.CreateList(b, RuleListCreate{Name: "vip", Kind: "domains", Entries: []string{"other.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateList(a, RuleListCreate{Name: "vip", Kind: "domains"}); errs.CodeOf(err) != errs.CodeRuleListNameTaken {
		t.Fatalf("dup name: %v", err)
	}
	for _, bad := range []RuleListCreate{{Name: "Bad Name", Kind: "domains"}, {Name: "ok", Kind: "nope"}} {
		if _, err := r.CreateList(a, bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("invalid %+v: %v", bad, err)
		}
	}
	if _, err := r.CreateList(asOrgW73(orgA, tenancy.RoleViewer), RuleListCreate{Name: "x", Kind: "keywords"}); err == nil {
		t.Fatal("viewer created a list")
	}
	// Never visible across organisations.
	if _, err := r.GetList(b, la.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if _, err := r.UpdateList(b, la.ID, RuleListUpdate{Entries: &[]string{"evil.com"}}, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant update: %v", err)
	}
	if err := r.DeleteList(b, la.ID, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	pageA, err := r.ListLists(a, 0)
	if err != nil || len(pageA.Data) != 1 || pageA.Data[0].ID != la.ID {
		t.Fatalf("list A: %+v %v", pageA, err)
	}
	empty, err := r.ListLists(asOrgW73(store.NewID(store.PrefixOrganization), tenancy.RoleAdmin), 0)
	if err != nil || empty.Data == nil || len(empty.Data) != 0 {
		t.Fatalf("empty page must be []: %+v %v", empty, err)
	}

	// Same rule, same list name, different organisations → different results.
	prg, err := r.Engine.Compile(`inList("vip", fromDomain())`, "")
	if err != nil {
		t.Fatal(err)
	}
	ev := map[string]any{"type": "message.received", "data": map[string]any{"from": map[string]any{"address": "x@partner.com"}}}
	snapA, _ := r.Lists.Snapshot(a, orgA)
	snapB, _ := r.Lists.Snapshot(b, orgB)
	if m, _, _, err := r.Engine.EvalWithLists(a, prg, ev, nil, nil, snapA); err != nil || !m {
		t.Fatalf("org A inList: %v %v", m, err)
	}
	if m, _, _, err := r.Engine.EvalWithLists(b, prg, ev, nil, nil, snapB); err != nil || m {
		t.Fatalf("org B must not see A's entries: %v %v", m, err)
	}
	// A change takes effect without editing the rule (the cache is invalidated on write).
	upd, err := r.UpdateList(a, la.ID, RuleListUpdate{Entries: &[]string{"elsewhere.com"}}, formatETag(la.Version))
	if err != nil || upd.Version != 2 {
		t.Fatalf("update: %+v %v", upd, err)
	}
	if _, err := r.UpdateList(a, la.ID, RuleListUpdate{Entries: &[]string{"x.com"}}, formatETag(1)); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("stale If-Match: %v", err)
	}
	snapA, _ = r.Lists.Snapshot(a, orgA)
	if m, _, _, _ := r.Engine.EvalWithLists(a, prg, ev, nil, nil, snapA); m {
		t.Fatal("list change not picked up")
	}
	if err := r.DeleteList(a, la.ID, formatETag(upd.Version)); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetList(b, lb.ID); got == nil || got.Name != "vip" {
		t.Fatal("org B list affected by org A delete")
	}
}

func TestIntegrationW73StatsTestEventsCrossTenant(t *testing.T) {
	r, db := w73Repo(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	a, b := asOrgW73(orgA, tenancy.RoleAdmin), asOrgW73(orgB, tenancy.RoleAdmin)

	// The flusher writes batches on its own (no read needed) and once more on shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Stats.Run(ctx); close(done) }()
	for i := range 3 {
		r.Stats.Observe(orgA, "route", "rte_shared", "evt_a"+string(rune('0'+i)), i != 1, "", "")
	}
	r.Stats.Observe(orgA, "route", "rte_shared", "evt_err", false, "map failed", "")
	r.Stats.Observe(orgB, "route", "rte_b", "evt_b", true, "", "")
	cancel()
	<-done

	var doc bson.M
	if err := db.Collection(RuleStatsCollection).FindOne(context.Background(), bson.M{"_id": orgA + ":route:rte_shared"}).Decode(&doc); err != nil {
		t.Fatalf("flusher did not write: %v", err)
	}
	st, err := r.GetStats(a, "route", "rte_shared")
	if err != nil || st.Evaluated != 4 || st.Matched != 2 || st.Skipped != 1 || st.Errors != 1 || st.LastError != "map failed" {
		t.Fatalf("stats A = %+v %v", st, err)
	}
	if st, err := r.GetStats(b, "route", "rte_shared"); err != nil || st.Evaluated != 0 {
		t.Fatalf("org B sees A's counters: %+v %v", st, err)
	}
	dec, err := r.ListDecisions(a, "route", "rte_shared", 0)
	if err != nil || len(dec.Data) != 4 {
		t.Fatalf("decisions A = %+v %v", dec, err)
	}
	if dec, err := r.ListDecisions(b, "route", "rte_shared", 0); err != nil || len(dec.Data) != 0 {
		t.Fatalf("org B sees A's decisions: %+v %v", dec, err)
	}
	// Decisions are capped per rule.
	for i := range maxDecisionsPerRule + 10 {
		r.Stats.Observe(orgA, "endpoint", "whe_cap", "evt_"+time.Now().Format("150405.000000000")+string(rune('a'+i%26)), true, "", "")
	}
	r.Stats.Flush(context.Background())
	if n, _ := db.Collection(RuleDecisionsCollection).CountDocuments(context.Background(), bson.M{"organizationId": orgA, "refId": "whe_cap"}); n != maxDecisionsPerRule {
		t.Fatalf("decisions kept = %d, want %d", n, maxDecisionsPerRule)
	}

	// Test-event picker: own organisation only, subject read from the stored JSON data.
	now := time.Now().UTC()
	insert := func(id, org, typ, subject string) {
		raw, _ := json.Marshal(map[string]any{"subject": subject})
		if _, err := db.Collection("events").InsertOne(context.Background(), bson.M{
			"_id": id, "organizationId": org, "accountId": "acc_1", "type": typ, "time": now,
			"subject": "msg_x", "data": string(raw), "schema": 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("evt_pa1", orgA, "message.received", "Invoice 1")
	insert("evt_pa2", orgA, "message.sent", "Out 1")
	insert("evt_pb1", orgB, "message.received", "Secret of B")
	list, err := r.ListTestEvents(a, TestEventsFilter{})
	if err != nil || len(list.Data) != 2 {
		t.Fatalf("test events A = %+v %v", list, err)
	}
	for _, row := range list.Data {
		if row.ID == "evt_pb1" {
			t.Fatal("org B event leaked into org A picker")
		}
		if row.ID == "evt_pa1" && row.Subject != "Invoice 1" {
			t.Fatalf("subject = %q", row.Subject)
		}
	}
	only, err := r.ListTestEvents(a, TestEventsFilter{Type: "message.received", AccountID: "acc_1", Limit: 5})
	if err != nil || len(only.Data) != 1 || only.Data[0].ID != "evt_pa1" {
		t.Fatalf("filtered = %+v %v", only, err)
	}
	// /test with {sample} and an unknown sample type.
	res, err := r.TestProgram(a, orgA, "endpoint:whe_x", 1, `type == "message.sent" && data.outboundId != ""`, `{"o": data.outboundId}`,
		RouteTestRequest{Sample: "message.sent"})
	if err != nil || !res.Matched || res.Mapped == nil {
		t.Fatalf("sample test = %+v %v", res, err)
	}
	if _, err := r.TestProgram(a, orgA, "endpoint:whe_x", 1, `true`, ``, RouteTestRequest{Sample: "no.such"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unknown sample: %v", err)
	}
}

func TestIntegrationW73NamedListLimits(t *testing.T) {
	r, _ := w73Repo(t)
	org := store.NewID(store.PrefixOrganization)
	a := asOrgW73(org, tenancy.RoleAdmin)
	big := make([]string, maxEntriesPerList+1)
	for i := range big {
		big[i] = "d" + time.Duration(i).String() + ".example"
	}
	if _, err := r.CreateList(a, RuleListCreate{Name: "big", Kind: "domains", Entries: big}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("too many entries: %v", err)
	}
	long := make([]byte, maxEntryBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := r.CreateList(a, RuleListCreate{Name: "long", Kind: "keywords", Entries: []string{string(long)}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("entry too long: %v", err)
	}
	l, err := r.CreateList(a, RuleListCreate{Name: "kw", Kind: "keywords", Entries: []string{"urgent"}})
	if err != nil {
		t.Fatal(err)
	}
	bad := "Not Valid"
	if _, err := r.UpdateList(a, l.ID, RuleListUpdate{Name: &bad}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("rename invalid: %v", err)
	}
	if _, err := r.UpdateList(a, l.ID, RuleListUpdate{Entries: &big}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("update too many: %v", err)
	}
	other, err := r.CreateList(a, RuleListCreate{Name: "kw2", Kind: "keywords"})
	if err != nil {
		t.Fatal(err)
	}
	taken := "kw"
	if _, err := r.UpdateList(a, other.ID, RuleListUpdate{Name: &taken}, ""); errs.CodeOf(err) != errs.CodeRuleListNameTaken {
		t.Fatalf("rename to taken: %v", err)
	}
	renamed := "kw-renamed"
	if got, err := r.UpdateList(a, other.ID, RuleListUpdate{Name: &renamed}, ""); err != nil || got.Name != renamed {
		t.Fatalf("rename: %+v %v", got, err)
	}
	if err := r.DeleteList(a, l.ID, formatETag(9)); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("stale delete: %v", err)
	}
	if err := r.DeleteList(asOrgW73(org, tenancy.RoleViewer), l.ID, ""); err == nil {
		t.Fatal("viewer deleted a list")
	}
	for i := 2; i < maxListsPerOrg; i++ {
		if _, err := r.CreateList(a, RuleListCreate{Name: "q" + time.Duration(i).String()[:1] + string(rune('a'+i%26)) + string(rune('a'+i/26)), Kind: "keywords"}); err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
	}
	if _, err := r.CreateList(a, RuleListCreate{Name: "one-too-many", Kind: "keywords"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("quota: %v", err)
	}
	page, err := r.ListLists(a, 500)
	if err != nil || len(page.Data) != maxListsPerOrg {
		t.Fatalf("page = %d %v", len(page.Data), err)
	}
}
