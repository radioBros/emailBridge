package connect

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
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
		URI: uri, Database: fmt.Sprintf("ebtest_connect_%d", time.Now().UnixNano()), ConnectTimeout: 3 * time.Second,
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

func TestRepoMongoRoundTrip(t *testing.T) {
	db := testMongo(t)
	ctx := context.Background()
	if err := EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	r := &Repo{DB: db}
	if Migration(36).Up == nil {
		t.Fatal()
	}
	id := store.NewID(store.PrefixConnectLink)
	link := &Link{
		ID: id, OrganizationID: "org_1", Mode: ModeHosted, TokenHash: "abc",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	if err := r.InsertLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetLink(ctx, id)
	if err != nil || got.Mode != ModeHosted {
		t.Fatal(err, got)
	}
	if err := r.MarkLinkUsed(ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkLinkUsed(ctx, id, time.Now()); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
	if _, err := r.GetLink(ctx, store.NewID(store.PrefixConnectLink)); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}

	orgID := store.NewID(store.PrefixOrganization)
	_, _ = db.Collection("organizations").InsertOne(ctx, orgDoc{
		ID: orgID, Name: "Org", Version: 1, Branding: map[string]any{"brandName": "B"},
	})
	o, err := r.GetOrg(ctx, orgID)
	if err != nil || o.Name != "Org" {
		t.Fatal(err, o)
	}
	updated, err := r.SetOrgBranding(ctx, orgID, map[string]any{"brandName": "B2", "headerHtml": "<p>x</p>"}, 1)
	if err != nil || updated.Version != 2 || updated.Branding["brandName"] != "B2" {
		t.Fatalf("%+v %v", updated, err)
	}
	if _, err := r.SetOrgBranding(ctx, orgID, map[string]any{"brandName": "B3"}, 1); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}
	if _, err := r.GetOrg(ctx, store.NewID(store.PrefixOrganization)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	appID := store.NewID(store.PrefixOAuthApp)
	_, _ = db.Collection("oauthApps").InsertOne(ctx, map[string]any{
		"_id": appID, "organizationId": orgID, "name": "G", "provider": "gmail", "enabled": true,
	})
	apps, err := r.ListOAuthApps(ctx, orgID)
	if err != nil || len(apps) != 1 {
		t.Fatal(err, apps)
	}

	accID := store.NewID(store.PrefixAccount)
	_, _ = db.Collection("accounts").InsertOne(ctx, accountDoc{
		ID: accID, OrganizationID: orgID, Email: "a@b.c", Version: 1,
	})
	a, err := r.GetAccount(ctx, orgID, accID)
	if err != nil {
		t.Fatal(err)
	}
	pin := Pin(Observed{Email: "a@b.c", IMAPUser: "a@b.c"}, time.Now())
	if err := r.SetIdentity(ctx, orgID, accID, pin, a.Version); err != nil {
		t.Fatal(err)
	}
	if err := r.SetIdentity(ctx, orgID, accID, pin, a.Version); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}
	if _, err := r.GetAccount(ctx, orgID, store.NewID(store.PrefixAccount)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}
