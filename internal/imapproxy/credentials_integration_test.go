//go:build integration

package imapproxy

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_imapproxy_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
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
	hash, _ := hashPassword("long-enough-password")
	if err := r.Upsert(ctx, org, acc, hash, "u"); err != nil {
		t.Fatal(err)
	}
	c, err := r.Get(ctx, acc)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	_ = r.Delete(ctx, acc)
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
	m := &Module{Options: Options{Enabled: false}, Env: "test", Gate: alwaysOn{}, Accounts: &memAccounts{}, Source: &memSource{}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(context.Background())
}
