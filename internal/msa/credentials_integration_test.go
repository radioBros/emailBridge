//go:build integration

package msa

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
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
		URI: uri, Database: fmt.Sprintf("ebtest_msa_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRepoCRUD(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := &Repo{DB: db}
	acc := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	c, err := r.Get(ctx, acc)
	if err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
	hash, _ := hashPassword("long-enough-password")
	if err := r.Upsert(ctx, org, acc, hash, "usr_1"); err != nil {
		t.Fatal(err)
	}
	c, err = r.Get(ctx, acc)
	if err != nil || c == nil || c.Hash != hash {
		t.Fatalf("%+v %v", c, err)
	}
	if err := r.Delete(ctx, acc); err != nil {
		t.Fatal(err)
	}
	c, err = r.Get(ctx, acc)
	if err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
}

func TestModuleStart(t *testing.T) {
	db := testDB(t)
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, db)
	app.Provide(a, srv)
	m := &Module{
		Options:  Options{Enabled: false},
		Env:      "test",
		Accounts: &memAccounts{},
		Gate:     alwaysOn{},
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), store.NewID(store.PrefixOrganization)), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_x", Role: tenancy.RoleAdmin,
	})
	_ = ctx
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// non-api
	m2 := &Module{}
	a2 := app.New(app.Options{Roles: app.RoleSet{}})
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
}
