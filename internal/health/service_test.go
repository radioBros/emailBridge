package health

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testMongo(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_health_%d", time.Now().UnixNano()),
		ConnectTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = db.DB.Drop(cctx)
		_ = db.Close(cctx)
	})
	return db
}

func orgCtx(org string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
	})
}

func insertAccount(t *testing.T, db *store.DB, a accounts.Account) {
	t.Helper()
	if _, err := db.Collection(index.Accounts).InsertOne(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func TestServiceGetListCheck(t *testing.T) {
	db := testMongo(t)
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	syncAt := now.Add(-time.Hour)
	idOK := store.NewID(store.PrefixAccount)
	idBad := store.NewID(store.PrefixAccount)
	insertAccount(t, db, accounts.Account{
		ID: idOK, OrganizationID: org, Name: "Ok Mail", Email: "ok@example.com",
		State: accounts.StateConnected, LastSyncAt: &syncAt,
		IMAP:         accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit"},
		Capabilities: map[string]any{"IDLE": true},
		CreatedAt:    now, UpdatedAt: now, Version: 1,
	})
	insertAccount(t, db, accounts.Account{
		ID: idBad, OrganizationID: org, Name: "Bad Gmail", Email: "bad@gmail.com",
		State:       accounts.StateAuthError,
		StateReason: &accounts.StateReason{Kind: "auth", Code: "mail_auth_failed", Message: "NO", At: now},
		IMAP:        accounts.IMAPConfig{Host: "imap.gmail.com", Port: 993, TLS: "implicit"},
		CreatedAt:   now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(index.Mailboxes).InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixMailbox), "accountId": idOK, "status": "active", "path": "INBOX",
	})

	svc := &Service{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}

	h, err := svc.Get(ctx, idOK)
	if err != nil || h.Severity != SeverityOK || h.Facts.FolderCount != 1 {
		t.Fatalf("get ok: %+v %v", h, err)
	}
	h2, err := svc.Get(ctx, idBad)
	if err != nil || h2.Severity != SeverityCritical || !hasCode(h2.Hints, HintAppPassword) {
		t.Fatalf("get bad: %+v %v", h2, err)
	}
	if _, err := svc.Get(ctx, store.NewID(store.PrefixAccount)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing: %v", err)
	}

	list, err := svc.List(ctx, ListFilter{Page: 1, Limit: 10, IncludeTotal: true, Sort: "-severity", Q: "gmail"})
	if err != nil || list.Counts.Total != 1 || list.Total == nil || *list.Total != 1 {
		t.Fatalf("list q: %+v %v", list, err)
	}
	list2, err := svc.List(ctx, ListFilter{Page: 1, Limit: 10, IncludeTotal: true, Severity: SeverityCritical})
	if err != nil || len(list2.Data) != 1 || list2.Counts.Critical != 1 || list2.Counts.Total != 2 {
		t.Fatalf("list sev: %+v %v", list2, err)
	}
	list3, err := svc.List(ctx, ListFilter{Page: 1, Limit: 1, Sort: "name"})
	if err != nil || len(list3.Data) != 1 || list3.Data[0].Name != "Bad Gmail" {
		t.Fatalf("page: %+v %v", list3, err)
	}

	// Live check without pool → configured error path for IMAP.
	hc, err := svc.Check(ctx, idOK)
	if err != nil || hc.LiveCheck == nil || hc.LiveCheck.OK {
		t.Fatalf("check no pool: %+v %v", hc, err)
	}

	// API provider path
	idAPI := store.NewID(store.PrefixAccount)
	insertAccount(t, db, accounts.Account{
		ID: idAPI, OrganizationID: org, Name: "Graph", Email: "g@contoso.com",
		State: accounts.StateConnected, Provider: "graph", OAuth: &accounts.OAuthLink{},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	ha, err := svc.Check(ctx, idAPI)
	if err != nil || ha.LiveCheck == nil || !ha.LiveCheck.OK {
		t.Fatalf("api check: %+v %v", ha, err)
	}

	// Rate limit
	svc.checkRL = map[string]*tokenBucket{org: {tokens: 0, last: now}}
	if err := svc.takeCheckToken(context.Background(), org); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("rate: %v", err)
	}
	svc.checkRL[org].tokens = 5
	svc.checkRL[org].last = now.Add(-2 * time.Minute)
	if err := svc.takeCheckToken(context.Background(), org); err != nil {
		t.Fatal(err)
	}
}

func TestModuleStartRegister(t *testing.T) {
	db := testMongo(t)
	roles, err := app.ParseRoles("api")
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: roles, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Skipf("httpapi: %v", err)
	}
	app.Provide(a, srv)
	m := &Module{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if m.Name() != "health" || len(m.Roles()) != 1 {
		t.Fatal(m.Name(), m.Roles())
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Non-API role: Start still provides service, skips route registration.
	a2 := app.New(app.Options{Roles: app.RoleSet{}, NodeID: "n2", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a2, db)
	m2 := &Module{}
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
	if m2.Service() == nil {
		t.Fatal("svc2")
	}
}

func TestSortEdge(t *testing.T) {
	rows := []Row{
		{AccountID: "2", Name: "b", State: "connected", LastSyncAt: ptrTime(time.Unix(2, 0))},
		{AccountID: "1", Name: "a", State: "authError", LastSyncAt: ptrTime(time.Unix(1, 0))},
		{AccountID: "3", Name: "c", State: "new"},
	}
	sortRows(rows, "lastSyncAt")
	if rows[0].AccountID != "3" { // nil first
		t.Fatalf("%+v", rows)
	}
	sortRows(rows, "-state")
	sortRows(rows, "")
	if Severity("").Rank() != 0 {
		t.Fatal()
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestAPIValidation(t *testing.T) {
	m := &Module{svc: &Service{}}
	_, err := m.listHealth(context.Background(), &listIn{Severity: "nope"})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	_, err = m.listHealth(context.Background(), &listIn{Sort: "nope"})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestAPIHandlers(t *testing.T) {
	db := testMongo(t)
	org := store.NewID(store.PrefixOrganization)
	ctx := orgCtx(org)
	now := time.Now().UTC()
	id := store.NewID(store.PrefixAccount)
	insertAccount(t, db, accounts.Account{
		ID: id, OrganizationID: org, Name: "H", Email: "h@example.com",
		State: accounts.StateConnected, CreatedAt: now, UpdatedAt: now, Version: 1,
		IMAP: accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit"},
	})
	m := &Module{svc: &Service{DB: db, Now: func() time.Time { return now }}}
	out, err := m.getHealth(ctx, &accountPath{AccountID: id})
	if err != nil || out.Body.AccountID != id {
		t.Fatalf("%+v %v", out, err)
	}
	out2, err := m.checkHealth(ctx, &accountPath{AccountID: id})
	if err != nil || out2.Body.LiveCheck == nil {
		t.Fatalf("%+v %v", out2, err)
	}
	list, err := m.listHealth(ctx, &listIn{Page: 1, Limit: 10, IncludeTotal: true, Sort: "-severity", Severity: "ok"})
	if err != nil || list.Body.Counts.Total < 1 {
		t.Fatalf("%+v %v", list, err)
	}
}

func TestLiveErrMapping(t *testing.T) {
	cases := []struct {
		msg  string
		code string
	}{
		{"tls: handshake failure", string(errs.CodeMailTLSFailed)},
		{"i/o timeout", string(errs.CodeMailTimeout)},
		{"AUTHENTICATIONFAILED", string(errs.CodeMailAuthFailed)},
		{"connection refused", string(errs.CodeMailConnectFailed)},
	}
	for _, c := range cases {
		lc := liveErr(plainErr(c.msg), 1)
		if lc.Error.Code != c.code {
			t.Fatalf("%s → %s want %s", c.msg, lc.Error.Code, c.code)
		}
	}
	long := plainErr(string(make([]byte, 300)))
	lc := liveErr(long, 0)
	if len(lc.Error.Message) > 240 {
		t.Fatal(len(lc.Error.Message))
	}
}

func TestSummarizeBranches(t *testing.T) {
	if summarize(SeverityOK, accounts.StateNew, nil, false) != "Not connected yet" {
		t.Fatal()
	}
	for _, st := range []string{accounts.StateConnecting, accounts.StateSyncing, accounts.StateDisabled, accounts.StateDeleting} {
		if summarize(SeverityInfo, st, nil, false) == "" {
			t.Fatal(st)
		}
	}
	if summarize(SeverityWarning, accounts.StateConnectError, nil, false) != "Connection issues" {
		t.Fatal()
	}
	if summarize(SeverityCritical, accounts.StateAuthError, nil, false) != "Critical account issue" {
		t.Fatal()
	}
}

func TestWrongHostGmailMismatch(t *testing.T) {
	a := &accounts.Account{
		Email: "u@gmail.com", State: accounts.StateConnectError,
		IMAP: accounts.IMAPConfig{Host: "mail.wrong.com", Port: 993, TLS: "implicit"},
	}
	if !wrongHostPortTLS(a, "mail_connect_failed", "") {
		t.Fatal()
	}
	a2 := &accounts.Account{
		Email: "u@x.com", State: accounts.StateConnectError,
		IMAP: accounts.IMAPConfig{Host: "imap.x.com", Port: 143, TLS: "implicit"},
	}
	if !wrongHostPortTLS(a2, "", "") {
		t.Fatal("143+implicit")
	}
}

// TestListCacheScopingAndTTL: the org diagnosis is cached for listCacheTTL (a change shows after the TTL or a
// live check), and a resource-limited API key sees counts and rows of its accounts only — never the
// organisation's other accounts, nor another organisation's.
func TestListCacheScopingAndTTL(t *testing.T) {
	db := testMongo(t)
	org := store.NewID(store.PrefixOrganization)
	other := store.NewID(store.PrefixOrganization)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	svc := &Service{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return clock }}
	mk := func(o, name, state string) string {
		id := store.NewID(store.PrefixAccount)
		syncAt := now.Add(-time.Minute)
		insertAccount(t, db, accounts.Account{
			ID: id, OrganizationID: o, Name: name, Email: name + "@example.com", State: state, LastSyncAt: &syncAt,
			IMAP:         accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit"},
			Capabilities: map[string]any{"IDLE": true}, CreatedAt: now, UpdatedAt: now, Version: 1,
		})
		return id
	}
	a1 := mk(org, "alpha", accounts.StateConnected)
	mk(org, "beta", accounts.StateConnected)
	mk(other, "gamma", accounts.StateConnected)
	ctx := orgCtx(org)

	l, err := svc.List(ctx, ListFilter{})
	if err != nil || l.Counts.Total != 2 || l.Counts.OK != 2 {
		t.Fatalf("first list: %+v %v", l, err)
	}
	// A state change inside the TTL is not visible yet (cached) …
	if _, err := db.Collection(index.Accounts).UpdateOne(context.Background(), bson.M{"_id": a1},
		bson.M{"$set": bson.M{"state": accounts.StateAuthError, "stateReason": accounts.StateReason{Kind: "auth", Code: "mail_auth_failed", At: now}}}); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(listCacheTTL - time.Second)
	if l, _ = svc.List(ctx, ListFilter{}); l.Counts.Critical != 0 {
		t.Fatalf("cache bypassed: %+v", l.Counts)
	}
	// … and is after it.
	clock = now.Add(listCacheTTL + time.Second)
	if l, _ = svc.List(ctx, ListFilter{}); l.Counts.Critical != 1 || l.Counts.Total != 2 {
		t.Fatalf("after TTL: %+v", l.Counts)
	}
	// A live check drops the cache at once.
	if _, err := db.Collection(index.Accounts).UpdateOne(context.Background(), bson.M{"_id": a1},
		bson.M{"$set": bson.M{"state": accounts.StateConnected}, "$unset": bson.M{"stateReason": ""}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Check(ctx, a1); err != nil {
		t.Fatal(err)
	}
	if l, _ = svc.List(ctx, ListFilter{}); l.Counts.Critical != 0 {
		t.Fatalf("after live check: %+v", l.Counts)
	}

	// API key limited to one account: counts and rows of that account only (from the same cache).
	keyCtx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_1", OrganizationID: org, Role: tenancy.RoleViewer, AccountID: a1,
		Scopes: []string{"accounts:read"},
	})
	kl, err := svc.List(keyCtx, ListFilter{IncludeTotal: true})
	if err != nil || kl.Counts.Total != 1 || len(kl.Data) != 1 || kl.Data[0].AccountID != a1 || *kl.Total != 1 {
		t.Fatalf("key-limited list: %+v %v", kl, err)
	}
	// q is case-insensitive over name and email; the other organisation never appears.
	if ql, _ := svc.List(ctx, ListFilter{Q: "BETA"}); ql.Counts.Total != 1 || ql.Data[0].Name != "beta" {
		t.Fatalf("q: %+v", ql)
	}
	if ql, _ := svc.List(ctx, ListFilter{Q: "gamma"}); ql.Counts.Total != 0 {
		t.Fatalf("cross-org leak: %+v", ql)
	}
}
