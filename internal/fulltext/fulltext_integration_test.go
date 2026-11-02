//go:build integration

package fulltext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_fulltext_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, mg := range []store.Migration{jobs.Migration(8), accounts.Migration(9), index.Migration(10), Migration(49)} {
		if err := mg.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func optsUpsert() *options.UpdateOneOptionsBuilder { return options.UpdateOne().SetUpsert(true) }

var uidSeq atomic.Uint32

// fakeFetcher returns canned bodies and counts calls.
type fakeFetcher struct {
	mu     sync.Mutex
	bodies map[string][2]string // messageID â†’ plain, html
	errs   map[string]error
	calls  int
}

func (f *fakeFetcher) FetchText(ctx context.Context, accountID, messageID string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	// The indexer must call with a system caller of the right organisation (messages checks it).
	if c, ok := tenancy.CallerFrom(ctx); !ok || c.Kind != tenancy.CallerSystem {
		return "", "", fmt.Errorf("no system caller")
	}
	if err := f.errs[messageID]; err != nil {
		return "", "", err
	}
	b := f.bodies[messageID]
	return b[0], b[1], nil
}

// recorder is an Observer.
type recorder struct {
	mu       sync.Mutex
	indexed  []string
	removals []Removal
}

func (r *recorder) Indexed(_ context.Context, row *Row) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexed = append(r.indexed, row.MessageID)
}

func (r *recorder) Removed(_ context.Context, rm Removal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removals = append(r.removals, rm)
}

type fixture struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	fetch *fakeFetcher
	obs   *recorder
	org   string
	ctx   context.Context
}

func newFixture(t *testing.T) *fixture {
	db := testDB(t)
	f := &fixture{t: t, db: db, fetch: &fakeFetcher{bodies: map[string][2]string{}, errs: map[string]error{}},
		obs: &recorder{}, org: store.NewID(store.PrefixOrganization), ctx: context.Background()}
	f.svc = &Service{
		DB: db, Repo: &Repo{DB: db}, Fetcher: f.fetch, MinInterval: -1, Observers: []Observer{f.obs},
		Switches: &Switches{DB: db, Registry: features.DefaultRegistry(), Env: "test", TTL: -1},
		Jobs:     &jobs.Service{DB: db},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return f
}

func (f *fixture) setOrgSwitch(on any) {
	f.t.Helper()
	_, err := f.db.Collection(settings.SettingsCollection).UpdateOne(f.ctx, bson.M{"_id": settings.OrgDocID(f.org)},
		bson.M{"$set": bson.M{"switches": bson.M{SwitchName: bson.M{"value": on}}}}, optsUpsert())
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) account(org string, switches map[string]any) string {
	f.t.Helper()
	id := store.NewID(store.PrefixAccount)
	if _, err := f.db.Collection(accounts.Collection).InsertOne(f.ctx, bson.M{
		"_id": id, "organizationId": org, "state": "connected", "switches": switches, "name": "A", "email": id + "@x.test",
	}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// message indexes a message and its folder and gives it a body.
func (f *fixture) message(org, acc, subject, plain, htmlBody string, date time.Time) string {
	f.t.Helper()
	mbx := "mbx_" + strings.TrimPrefix(acc, "acc_")
	_, _ = f.db.Collection(index.Mailboxes).UpdateOne(f.ctx, bson.M{"_id": mbx},
		bson.M{"$set": bson.M{"organizationId": org, "accountId": acc, "path": "INBOX", "status": "active"}}, optsUpsert())
	time.Sleep(2 * time.Millisecond) // message IDs sort in creation order (the reindex walks newest first)
	id := store.NewID(store.PrefixMessage)
	d := date
	msg := index.Message{
		ID: id, OrganizationID: org, AccountID: acc, MailboxID: mbx, UID: uidSeq.Add(1),
		EmailID: id, InternalDate: date.Add(time.Minute), Size: int64(len(plain) + 200), IndexedAt: time.Now().UTC(),
		Subject: subject, From: &index.Address{Name: "Billing", Address: "billing@example.com"},
		To: []index.Address{{Address: "reception@example.com"}}, Date: &d,
	}
	if _, err := f.db.Collection(index.Messages).InsertOne(f.ctx, msg); err != nil {
		f.t.Fatal(err)
	}
	f.fetch.mu.Lock()
	f.fetch.bodies[id] = [2]string{plain, htmlBody}
	f.fetch.mu.Unlock()
	return id
}

func (f *fixture) received(org, acc, msgID string) error {
	data, _ := json.Marshal(map[string]any{"accountId": acc, "messageId": msgID, "mailbox": map[string]any{"mailboxId": "mbx_x", "path": "INBOX"}})
	return f.svc.HandleEvent(f.ctx, events.Event{ID: store.NewID(store.PrefixEvent), Type: events.MessageReceived,
		EBOrg: org, EBAccount: acc, Data: data})
}

func (f *fixture) row(id string) *Row {
	f.t.Helper()
	var r Row
	if err := f.db.Collection(Collection).FindOne(f.ctx, bson.M{"_id": id}).Decode(&r); err != nil {
		return nil
	}
	return &r
}

func (f *fixture) count(filter bson.M) int64 {
	n, err := f.db.Collection(Collection).CountDocuments(f.ctx, filter)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func userCtx(org string) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_t", OrganizationID: org, Role: tenancy.RoleOwner})
}

func keyCtx(org string, c tenancy.Caller) context.Context {
	c.Kind, c.APIKeyID, c.OrganizationID = tenancy.CallerAPIKey, "key_t", org
	return tenancy.WithCaller(store.WithOrganization(context.Background(), org), c)
}

func TestIntegrationIndexerFromEvents(t *testing.T) {
	f := newFixture(t)
	f.setOrgSwitch(true)
	acc := f.account(f.org, nil)
	date := time.Date(2026, 10, 27, 9, 55, 12, 0, time.UTC)
	plainID := f.message(f.org, acc, "Invoice 2026-1043", "Please find the invoice attached.", "", date)
	htmlID := f.message(f.org, acc, "Newsletter", "", "<p>Hello <b>Marco</b></p><script>x()</script>", date)

	for _, id := range []string{plainID, htmlID, plainID} { // the repeat proves the upsert is idempotent
		if err := f.received(f.org, acc, id); err != nil {
			t.Fatal(err)
		}
	}
	r := f.row(plainID)
	if r == nil || r.MessageID != plainID || r.OrganizationID != f.org || r.AccountID != acc || r.Path != "INBOX" ||
		r.Subject != "Invoice 2026-1043" || r.From != "Billing <billing@example.com>" || len(r.To) != 1 ||
		!r.Date.Equal(date) || r.Text != "Please find the invoice attached." || r.IndexedAt.IsZero() {
		t.Fatalf("row: %+v", r)
	}
	if h := f.row(htmlID); h == nil || h.Text != "Hello Marco" {
		t.Fatalf("html row: %+v", h)
	}
	if n := f.count(bson.M{"accountId": acc}); n != 2 {
		t.Fatalf("rows=%d", n)
	}

	// Deleted message: row gone, observer told.
	data, _ := json.Marshal(map[string]any{"accountId": acc, "messageId": plainID, "mailbox": map[string]any{"mailboxId": r.MailboxID}})
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_d", Type: events.MessageDeleted, EBOrg: f.org, EBAccount: acc, Data: data}); err != nil {
		t.Fatal(err)
	}
	if f.row(plainID) != nil {
		t.Fatal("deleted message still indexed")
	}
	// A received event for a message no longer in the index writes nothing.
	if _, err := f.db.Collection(index.Messages).DeleteOne(f.ctx, bson.M{"_id": plainID}); err != nil {
		t.Fatal(err)
	}
	if err := f.received(f.org, acc, plainID); err != nil || f.row(plainID) != nil {
		t.Fatalf("gone message: %v", err)
	}
	// Mailbox deleted: the folder's rows go.
	data, _ = json.Marshal(map[string]any{"accountId": acc, "mailbox": map[string]any{"mailboxId": r.MailboxID}})
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_m", Type: events.MailboxDeleted, EBOrg: f.org, EBAccount: acc, Data: data}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(bson.M{"accountId": acc}); n != 0 {
		t.Fatalf("mailbox rows=%d", n)
	}
	f.obs.mu.Lock()
	if len(f.obs.indexed) != 3 || len(f.obs.removals) != 2 || f.obs.removals[1].MailboxID != r.MailboxID {
		t.Fatalf("observer: %v %+v", f.obs.indexed, f.obs.removals)
	}
	f.obs.mu.Unlock()

	// Provider errors come back for the consumer to classify.
	errID := f.message(f.org, acc, "Busy", "x", "", date)
	f.fetch.errs[errID] = errs.New(errs.CodeConnectionBusy, "busy")
	if err := f.received(f.org, acc, errID); errs.CodeOf(err) != errs.CodeConnectionBusy {
		t.Fatalf("busy: %v", err)
	}
	// Unrelated and malformed events.
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_o", Type: events.MessageSent, EBOrg: f.org, EBAccount: acc}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_b", Type: events.MessageReceived, EBOrg: f.org, EBAccount: acc, Data: []byte("{")}); err == nil {
		t.Fatal("malformed data accepted")
	}
}

func TestIntegrationSwitchGating(t *testing.T) {
	f := newFixture(t)
	date := time.Now().UTC().Truncate(time.Second)
	acc := f.account(f.org, nil)
	optOut := f.account(f.org, map[string]any{SwitchName: false})
	optIn := f.account(f.org, map[string]any{SwitchName: true})
	m1 := f.message(f.org, acc, "s", "alpha", "", date)
	m2 := f.message(f.org, optOut, "s", "alpha", "", date)
	m3 := f.message(f.org, optIn, "s", "alpha", "", date)

	// Organisation off (default): nothing is indexed, not even the account that opted in; search is refused.
	for _, p := range [][2]string{{acc, m1}, {optOut, m2}, {optIn, m3}} {
		if err := f.received(f.org, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(bson.M{}); n != 0 || f.fetch.calls != 0 {
		t.Fatalf("indexed with the switch off: rows=%d fetches=%d", n, f.fetch.calls)
	}
	if _, err := f.svc.Search(userCtx(f.org), SearchInput{Q: "alpha"}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("search while off: %v", err)
	}
	if _, err := f.svc.StartReindex(userCtx(f.org), acc, 0); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("reindex while off: %v", err)
	}

	// Organisation on: everyone but the opt-out.
	f.setOrgSwitch(true)
	for _, p := range [][2]string{{acc, m1}, {optOut, m2}, {optIn, m3}} {
		if err := f.received(f.org, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if f.row(m1) == nil || f.row(m2) != nil || f.row(m3) == nil {
		t.Fatal("opt-out not honoured")
	}
	if _, err := f.svc.StartReindex(userCtx(f.org), optOut, 0); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("reindex of an opted-out account: %v", err)
	}

	// The account opts out: account.updated purges it at once.
	if _, err := f.db.Collection(accounts.Collection).UpdateOne(f.ctx, bson.M{"_id": optIn},
		bson.M{"$set": bson.M{"switches": bson.M{SwitchName: false}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"accountId": optIn, "changed": []string{"name"}})
	_ = f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_u0", Type: events.AccountUpdated, EBOrg: f.org, EBAccount: optIn, Data: data})
	if f.row(m3) == nil {
		t.Fatal("an unrelated account change must not purge")
	}
	data, _ = json.Marshal(map[string]any{"accountId": optIn, "changed": []string{"switches"}})
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_u", Type: events.AccountUpdated, EBOrg: f.org, EBAccount: optIn, Data: data}); err != nil {
		t.Fatal(err)
	}
	if f.row(m3) != nil {
		t.Fatal("opted-out account not purged")
	}

	// The organisation turns it off: the sweep purges the rest. An organisation lock beats an opt-out.
	f.setOrgSwitch(false)
	n, err := f.svc.PurgeOnce(f.ctx)
	if err != nil || n != 1 || f.row(m1) != nil {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	if _, err := f.db.Collection(settings.SettingsCollection).UpdateOne(f.ctx, bson.M{"_id": settings.OrgDocID(f.org)},
		bson.M{"$set": bson.M{"switches": bson.M{SwitchName: bson.M{"value": true, "locked": true}}}}); err != nil {
		t.Fatal(err)
	}
	if on, err := f.svc.Switches.AccountEnabled(f.ctx, f.org, optOut); err != nil || !on {
		t.Fatalf("locked organisation value must win: %v %v", on, err)
	}
	// A deleting account is never indexed; another organisation's account is unknown here.
	if _, err := f.db.Collection(accounts.Collection).UpdateOne(f.ctx, bson.M{"_id": acc},
		bson.M{"$set": bson.M{"state": accounts.StateDeleting}}); err != nil {
		t.Fatal(err)
	}
	if on, _ := f.svc.Switches.AccountEnabled(f.ctx, f.org, acc); on {
		t.Fatal("deleting account enabled")
	}
	if on, _ := f.svc.Switches.AccountEnabled(f.ctx, store.NewID(store.PrefixOrganization), optIn); on {
		t.Fatal("account resolved under a foreign organisation")
	}
}

func TestIntegrationSearch(t *testing.T) {
	f := newFixture(t)
	f.setOrgSwitch(true)
	accA := f.account(f.org, nil)
	accB := f.account(f.org, nil)
	old := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	a1 := f.message(f.org, accA, "Invoice 2026-1043", "Please find the invoice attached. Payment due in 30 days.", "", recent)
	a2 := f.message(f.org, accA, "Lunch", "Shall we meet for lunch? Bring the fattura please.", "", old)
	b1 := f.message(f.org, accB, "Reminder", "This is a reminder about the unpaid invoice.", "", recent.Add(time.Hour))
	other := store.NewID(store.PrefixOrganization)
	accX := f.account(other, nil)
	x1 := f.message(other, accX, "Invoice", "invoice for another tenant", "", recent)
	for _, p := range [][3]string{{f.org, accA, a1}, {f.org, accA, a2}, {f.org, accB, b1}} {
		if err := f.received(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	// The other organisation's row (indexed directly; its switch is off).
	if err := f.svc.Repo.Upsert(f.ctx, &Row{OrganizationID: other, AccountID: accX, MessageID: x1, Subject: "Invoice",
		Text: "invoice for another tenant", Date: recent, IndexedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	ctx := userCtx(f.org)
	res, err := f.svc.Search(ctx, SearchInput{Q: "invoice"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || len(res.Data) != 2 || res.TotalCapped {
		t.Fatalf("total=%d len=%d %+v", res.Total, len(res.Data), res.Data)
	}
	// Subject weight: the message with invoice in the subject ranks first.
	if res.Data[0].MessageID != a1 || res.Data[1].MessageID != b1 {
		t.Fatalf("order: %s %s", res.Data[0].MessageID, res.Data[1].MessageID)
	}
	h := res.Data[0]
	if h.AccountID != accA || h.Path != "INBOX" || h.From != "Billing <billing@example.com>" || h.Score <= 0 ||
		!strings.Contains(h.Snippet, "invoice") || len(h.Highlights) != 1 {
		t.Fatalf("hit: %+v", h)
	}
	if got := string([]rune(h.Snippet)[h.Highlights[0].Start:h.Highlights[0].End]); got != "invoice" {
		t.Fatalf("highlight %q", got)
	}
	// Account filter, date range, phrase, exclusion, diacritics/case.
	for _, tc := range []struct {
		in   SearchInput
		want []string
	}{
		{SearchInput{Q: "invoice", AccountID: accB}, []string{b1}},
		{SearchInput{Q: "invoice", From: recent.Add(30 * time.Minute)}, []string{b1}},
		{SearchInput{Q: "invoice", To: recent}, []string{a1}},
		{SearchInput{Q: `"unpaid invoice"`}, []string{b1}},
		{SearchInput{Q: "invoice -reminder"}, []string{a1}},
		{SearchInput{Q: "FATTURA lunch"}, []string{a2}},
		{SearchInput{Q: "billing"}, []string{b1, a1, a2}}, // sender only: equal scores, newest first
		{SearchInput{Q: "tenant"}, nil},
	} {
		res, err := f.svc.Search(ctx, tc.in)
		if err != nil {
			t.Fatalf("%+v: %v", tc.in, err)
		}
		var got []string
		for _, h := range res.Data {
			got = append(got, h.MessageID)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%+v: got %v want %v", tc.in, got, tc.want)
		}
	}
	// Paging.
	res, err = f.svc.Search(ctx, SearchInput{Q: "billing", Page: 2, Limit: 2})
	if err != nil || len(res.Data) != 1 || res.Total != 3 || res.Data[0].MessageID != a2 {
		t.Fatalf("page 2: %+v %v", res, err)
	}

	// Account-bound key: only its account, never another.
	bound := keyCtx(f.org, tenancy.Caller{Scopes: []string{"messages:read"}, AccountID: accB})
	res, err = f.svc.Search(bound, SearchInput{Q: "invoice"})
	if err != nil || len(res.Data) != 1 || res.Data[0].AccountID != accB {
		t.Fatalf("bound key: %+v %v", res, err)
	}
	if _, err := f.svc.Search(bound, SearchInput{Q: "invoice", AccountID: accA}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key, other account: %v", err)
	}
	granular := keyCtx(f.org, tenancy.Caller{Scopes: []string{"messages:read:" + accA}})
	if _, err := f.svc.Search(granular, SearchInput{Q: "invoice"}); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("granular key without accountId: %v", err)
	}
	if res, err := f.svc.Search(granular, SearchInput{Q: "invoice", AccountID: accA}); err != nil || len(res.Data) != 1 {
		t.Fatalf("granular key with its account: %+v %v", res, err)
	}

	// Rows follow the index: a message that left it is dropped from results and deleted.
	if _, err := f.db.Collection(index.Messages).DeleteOne(f.ctx, bson.M{"_id": a1}); err != nil {
		t.Fatal(err)
	}
	res, err = f.svc.Search(ctx, SearchInput{Q: "invoice"})
	if err != nil || len(res.Data) != 1 || res.Data[0].MessageID != b1 || res.Total != 1 || f.row(a1) != nil {
		t.Fatalf("orphan: %+v %v", res, err)
	}
	if f.row(x1) == nil {
		t.Fatal("the orphan sweep must stay inside the caller's organisation")
	}
}

func TestIntegrationReindexJob(t *testing.T) {
	f := newFixture(t)
	f.setOrgSwitch(true)
	acc := f.account(f.org, nil)
	date := time.Now().UTC()
	var ids []string
	for i := range 5 {
		ids = append(ids, f.message(f.org, acc, fmt.Sprintf("m%d", i), fmt.Sprintf("body number%d", i), "", date))
	}
	// One already indexed, one failing permanently.
	if err := f.received(f.org, acc, ids[0]); err != nil {
		t.Fatal(err)
	}
	f.fetch.errs[ids[2]] = errs.New(errs.CodeMailProtocolError, "nope")

	ctx := userCtx(f.org)
	j, err := f.svc.StartReindex(ctx, acc, 4)
	if err != nil || j.Kind != JobKind || j.State != jobs.Queued {
		t.Fatalf("start: %+v %v", j, err)
	}
	// A second request returns the queued job.
	if again, err := f.svc.StartReindex(ctx, acc, 0); err != nil || again.ID != j.ID {
		t.Fatalf("dedupe: %+v %v", again, err)
	}
	if _, err := f.svc.StartReindex(ctx, acc, MaxReindexLimit+1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("limit: %v", err)
	}
	if _, err := f.svc.StartReindex(ctx, store.NewID(store.PrefixAccount), 0); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	viewer := tenancy.WithCaller(store.WithOrganization(context.Background(), f.org),
		tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_v", OrganizationID: f.org, Role: tenancy.RoleViewer})
	if _, err := f.svc.StartReindex(viewer, acc, 0); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer: %v", err)
	}

	f.svc.Jobs.Handle(JobKind, f.svc.handleReindex)
	out, err := f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc, Limit: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(ReindexResult)
	// Newest 4 by ID: ids[4..1] (ids[0], indexed already, is outside the limit); ids[2] fails.
	if res.Scanned != 4 || res.Indexed != 3 || res.Failed != 1 || res.AlreadyIndexed != 0 {
		t.Fatalf("result: %+v", res)
	}
	for _, id := range []string{ids[1], ids[3], ids[4]} {
		if f.row(id) == nil {
			t.Fatalf("%s not indexed", id)
		}
	}
	// Idempotent: a second run finds everything (but the failing one) indexed.
	out, err = f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(ReindexResult); res.Scanned != 5 || res.AlreadyIndexed != 4 || res.Indexed != 0 || res.Failed != 1 {
		t.Fatalf("second run: %+v", res)
	}
	// Cancellation stops the walk.
	cancelled := true
	if _, err := f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc}, func() bool { return cancelled }); err == nil {
		t.Fatal("cancelled job reported success")
	}
}

func TestIntegrationErasedWithTheAccount(t *testing.T) {
	f := newFixture(t)
	f.setOrgSwitch(true)
	acc := f.account(f.org, nil)
	keep := f.account(f.org, nil)
	m := f.message(f.org, acc, "s", "erase me", "", time.Now())
	k := f.message(f.org, keep, "s", "keep me", "", time.Now())
	for _, p := range [][2]string{{acc, m}, {keep, k}} {
		if err := f.received(f.org, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	js := &jobs.Service{DB: f.db}
	as := &accounts.Service{DB: f.db, Jobs: js}
	as.RegisterHandlers()
	if _, err := js.RunNow(f.ctx, "erasure", f.org, map[string]string{"accountId": acc, "organizationId": f.org}, nil); err != nil {
		t.Fatal(err)
	}
	if f.row(m) != nil || f.row(k) == nil {
		t.Fatal("account erasure must delete exactly the account's rows")
	}
	if e, ok := tenancy.RegistryByName(Collection); !ok || e.Class != tenancy.EraseBoth {
		t.Fatalf("registry entry: %+v", e)
	}
	found := false
	for _, c := range tenancy.OrgEraseCollections() {
		found = found || c == Collection
	}
	if !found {
		t.Fatal("organisation erasure does not cover messageText")
	}
	// account.deleted (after erasure) is a no-op purge that never fails.
	data, _ := json.Marshal(map[string]any{"accountId": acc})
	if err := f.svc.HandleEvent(f.ctx, events.Event{ID: "evt_x", Type: events.AccountDeleted, EBOrg: f.org, EBAccount: acc, Data: data}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationModuleHTTP(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, &messages.Service{DB: db})
	app.Provide(a, &jobs.Service{DB: db})
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Env: "test", Registry: features.DefaultRegistry(), Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	if m.Name() != "fulltext" || m.Service() == nil || m.Service().Fetcher == nil || m.Service().Jobs == nil {
		t.Fatal("module wiring")
	}
	org := store.NewID(store.PrefixOrganization)
	do := func(method, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil).WithContext(userCtx(org))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	w := do(http.MethodGet, "/v1/messages/search?q=invoice")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "feature_disabled") {
		t.Fatalf("off: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Collection(settings.SettingsCollection).InsertOne(ctx, bson.M{"_id": settings.OrgDocID(org),
		"switches": bson.M{SwitchName: bson.M{"value": true}}}); err != nil {
		t.Fatal(err)
	}
	m.Service().Switches.TTL = -1
	w = do(http.MethodGet, "/v1/messages/search?q="+url.QueryEscape(`"no such phrase"`)+"&from=2026-01-01T00:00:00Z")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("on: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/v1/messages/search"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing q: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/v1/messages/search?q=x&limit=500"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("limit: %d", w.Code)
	}
	acc := store.NewID(store.PrefixAccount)
	if _, err := db.Collection(accounts.Collection).InsertOne(ctx, bson.M{"_id": acc, "organizationId": org, "state": "connected"}); err != nil {
		t.Fatal(err)
	}
	w = do(http.MethodPost, "/v1/accounts/"+acc+"/fulltext/reindex?limit=10")
	if w.Code != http.StatusAccepted || !strings.HasPrefix(w.Header().Get("Location"), "/v1/jobs/job_") ||
		!strings.Contains(w.Body.String(), `"kind":"fulltext.reindex"`) {
		t.Fatalf("reindex: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
	// The admin UI posts without limit: the default applies.
	acc2 := store.NewID(store.PrefixAccount)
	if _, err := db.Collection(accounts.Collection).InsertOne(ctx, bson.M{"_id": acc2, "organizationId": org, "state": "connected"}); err != nil {
		t.Fatal(err)
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc2+"/fulltext/reindex"); w.Code != http.StatusAccepted ||
		!strings.Contains(w.Body.String(), `"kind":"fulltext.reindex"`) {
		t.Fatalf("reindex without limit: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc2+"/fulltext/reindex?limit=0"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("limit=0: %d %s", w.Code, w.Body.String())
	}
}
