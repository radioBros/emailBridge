//go:build integration

package ai

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/features"
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
		URI: uri, Database: fmt.Sprintf("ebtest_ai_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(29).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := crypto.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestIntegrationRepoModuleAPI(t *testing.T) {
	db := testDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = 9
	}
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{KEK: l, Store: &crypto.MongoKeyStore{DB: db}, NewID: func() string {
		return store.NewID(store.PrefixDataKey)
	}}

	repo := &Repo{DB: db}
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{
		Repo: repo, Cipher: cipher, Registry: features.DefaultRegistry(), Env: "test",
		Enabled:   func(context.Context, string, string) bool { return true },
		Providers: map[string]Provider{ProviderOpenAI: &MockProvider{}},
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	keyStr := "sk-int"
	v, err := svc.PutSettings(ctx, PutInput{Provider: ProviderOpenAI, Model: "gpt", APIKey: &keyStr})
	if err != nil || !v.APIKeySet {
		t.Fatal(v, err)
	}
	got, err := repo.Get(ctx, org)
	if err != nil || got == nil || got.Model != "gpt" {
		t.Fatal(got, err)
	}
	out, err := svc.Summarize(ctx, org, "hello")
	if err != nil || out == "" {
		t.Fatal(out, err)
	}

	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, db)
	app.Provide(a, cipher)
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Env: "test", Registry: features.DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	_ = tenancy.RoleOwner
}
