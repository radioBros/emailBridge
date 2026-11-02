//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/oauth"
	"github.com/radiobros/emailbridge/internal/store"
)

// fakeConnectAccounts inserts minimal account docs and records deletes.
type fakeConnectAccounts struct {
	db      *store.DB
	mu      sync.Mutex
	created []string
	deleted []string
}

func (f *fakeConnectAccounts) Create(ctx context.Context, in accounts.CreateInput) (*accounts.Account, error) {
	org, _ := store.OrganizationFrom(ctx)
	id := store.NewID(store.PrefixAccount)
	if _, err := f.db.Collection(accounts.Collection).InsertOne(context.Background(), bson.M{
		"_id": id, "organizationId": org, "email": in.Email, "name": in.Name,
	}); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.created = append(f.created, id)
	f.mu.Unlock()
	return &accounts.Account{ID: id, Email: in.Email}, nil
}

func (f *fakeConnectAccounts) Delete(_ context.Context, id, _ string) (*jobs.Job, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, id)
	f.mu.Unlock()
	return &jobs.Job{}, nil
}

type startedFlow struct{ accountID, appID, linkID, returnURL string }

type fakeLinkStarter struct {
	mu    sync.Mutex
	flows []startedFlow
}

func (f *fakeLinkStarter) StartForLink(_ context.Context, accountID, appID, linkID, returnURL string) (*oauth.StartResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flows = append(f.flows, startedFlow{accountID, appID, linkID, returnURL})
	return &oauth.StartResult{AuthorizeURL: "https://idp.example/auth?acc=" + accountID}, nil
}

func connectWiringDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_connoauth_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	return db
}

func insertConnectLink(t *testing.T, db *store.DB, org, mode, accountID string) string {
	t.Helper()
	id := store.NewID(store.PrefixConnectLink)
	doc := bson.M{"_id": id, "organizationId": org, "mode": mode, "tokenHash": "x",
		"expiresAt": time.Now().Add(time.Hour), "createdAt": time.Now()}
	if accountID != "" {
		doc["accountId"] = accountID
	}
	if _, err := db.Collection(connect.CollectionLinks).InsertOne(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	return id
}

func countAccounts(t *testing.T, db *store.DB, org string) int64 {
	t.Helper()
	n, err := db.Collection(accounts.Collection).CountDocuments(context.Background(), bson.M{"organizationId": org})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegrationHostedOAuthStart(t *testing.T) {
	db := connectWiringDB(t)
	org := store.NewID(store.PrefixOrganization)
	accs := &fakeConnectAccounts{db: db}
	starter := &fakeLinkStarter{}
	h := &hostedOAuth{db: db, accounts: accs, oauth: starter}
	ctx := context.Background()
	appID := store.NewID(store.PrefixOAuthApp)
	ret := "https://app.example.com/done"

	// oauth-mode link: the link's account is connected, nothing is created or re-pinned.
	bound := store.NewID(store.PrefixAccount)
	if _, err := db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": bound, "organizationId": org, "email": "owner@ex.test",
		"identity": bson.M{"email": "owner@ex.test"},
	}); err != nil {
		t.Fatal(err)
	}
	oauthLink := insertConnectLink(t, db, org, "", bound)
	u, err := h.start(ctx, org, "attacker@evil.test", appID, oauthLink, bound, ret)
	if err != nil || u == "" {
		t.Fatal(err)
	}
	if countAccounts(t, db, org) != 1 || len(accs.created) != 0 {
		t.Fatal("oauth-mode link must not create an account")
	}
	var pinned struct {
		Identity struct {
			Email string `bson:"email"`
		} `bson:"identity"`
	}
	_ = db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": bound}).Decode(&pinned)
	if pinned.Identity.Email != "owner@ex.test" {
		t.Fatalf("identity re-pinned to %q", pinned.Identity.Email)
	}
	if got := starter.flows[0]; got != (startedFlow{bound, appID, oauthLink, ret}) {
		t.Fatalf("flow = %+v", got)
	}

	// hosted link: one account per link, retries reuse it, another address is a conflict.
	hosted := insertConnectLink(t, db, org, connect.ModeHosted, "")
	if _, err := h.start(ctx, org, "new@ex.test", appID, hosted, "", ret); err != nil {
		t.Fatal(err)
	}
	if _, err := h.start(ctx, org, "NEW@ex.test", appID, hosted, "", ret); err != nil {
		t.Fatal(err)
	}
	if len(accs.created) != 1 || countAccounts(t, db, org) != 2 {
		t.Fatalf("hosted retries created %d accounts", len(accs.created))
	}
	if starter.flows[1].accountID != accs.created[0] || starter.flows[2].accountID != accs.created[0] {
		t.Fatalf("flows = %+v", starter.flows)
	}
	var l connect.Link
	if err := db.Collection(connect.CollectionLinks).FindOne(ctx, bson.M{"_id": hosted}).Decode(&l); err != nil {
		t.Fatal(err)
	}
	if l.PendingAccountID != accs.created[0] || l.AccountID != "" {
		t.Fatalf("link attach: %+v", l)
	}
	if _, err := h.start(ctx, org, "someone-else@ex.test", appID, hosted, "", ret); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("other address: %v", err)
	}

	// Concurrent first clicks: exactly one account stays attached; losers are deleted.
	race := insertConnectLink(t, db, org, connect.ModeHosted, "")
	before := len(accs.created)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.start(ctx, org, "race@ex.test", appID, race, "", ret)
		}()
	}
	wg.Wait()
	if err := db.Collection(connect.CollectionLinks).FindOne(ctx, bson.M{"_id": race}).Decode(&l); err != nil {
		t.Fatal(err)
	}
	made := len(accs.created) - before
	if l.PendingAccountID == "" || len(accs.deleted) != made-1 {
		t.Fatalf("race: attached %q, created %d, deleted %d", l.PendingAccountID, made, len(accs.deleted))
	}
	for _, d := range accs.deleted {
		if d == l.PendingAccountID {
			t.Fatal("the attached account was deleted")
		}
	}

	// Cross-tenant: a link of another organisation is invalid.
	if _, err := h.start(ctx, store.NewID(store.PrefixOrganization), "x@ex.test", appID, hosted, "", ret); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatalf("cross-tenant link: %v", err)
	}
	if _, err := (&hostedOAuth{db: db}).start(ctx, org, "x@ex.test", appID, hosted, "", ret); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no oauth: %v", err)
	}
}
