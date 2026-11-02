//go:build integration

package mcp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_mcp_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, db)
	app.Provide(a, srv)
	app.Provide(a, &accounts.Service{})
	app.Provide(a, &messages.Service{})
	app.Provide(a, &submit.Service{})
	app.Provide(a, &webhook.Service{})
	app.Provide(a, &events.Writer{DB: db})
	app.Provide(a, &audit.Writer{DB: db})
	m := &Module{Env: "test", Registry: features.DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Handler() == nil {
		t.Fatal("handler")
	}
}
