//go:build integration

package bus

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_bus_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func newApp(node string, db *store.DB) *app.App {
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: node, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a, db)
	return a
}

func TestIntegrationModuleEmbeddedLock(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m1 := &Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir()}}
	a1 := newApp("n1", db)
	if err := m1.Start(ctx, a1); err != nil {
		t.Fatal(err)
	}
	if m1.Name() != "bus" || m1.Roles() != nil {
		t.Fatal("identity")
	}
	if err := m1.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := app.MustGet[*Bus](a1)
	if err != nil || b == nil {
		t.Fatal("bus not provided")
	}
	// a second embedded node against the same database waits out the lock, then is refused while the
	// first node keeps it alive
	prevWait := embeddedLockWait
	embeddedLockWait = 2 * time.Second
	t.Cleanup(func() { embeddedLockWait = prevWait })
	m2 := &Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir()}}
	t0 := time.Now()
	err = m2.Start(ctx, newApp("n2", db))
	if err == nil || !strings.Contains(err.Error(), "EB_NATS_MODE=external") {
		t.Fatalf("second embedded node: %v", err)
	}
	if waited := time.Since(t0); waited < embeddedLockWait {
		t.Fatalf("second node gave up after %v, want a wait of %v", waited, embeddedLockWait)
	}
	if err := m1.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// after a clean stop the lock is free again
	m3 := &Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir()}}
	if err := m3.Start(ctx, newApp("n3", db)); err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = m3.Stop(ctx)
	// start failure (bad listen address) releases the lock
	m4 := &Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir(), Listen: "bad"}}
	if err := m4.Start(ctx, newApp("n4", db)); err == nil {
		t.Fatal("bad listen must fail")
	}
	m5 := &Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir()}}
	if err := m5.Start(ctx, newApp("n5", db)); err != nil {
		t.Fatalf("lock not released after failed start: %v", err)
	}
	_ = m5.Stop(ctx)
	// embedded mode without a store module fails clearly
	noDB := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "x"})
	if err := (&Module{Options: Options{Mode: ModeEmbedded, DataDir: t.TempDir()}}).Start(ctx, noDB); err == nil {
		t.Fatal("missing store must fail")
	}
}

func TestIntegrationModuleExternal(t *testing.T) {
	ctx := context.Background()
	srv, err := Open(ctx, Options{Mode: ModeEmbedded, NodeID: "srv", DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	m := &Module{Options: Options{Mode: ModeExternal, URL: srv.srv.ClientURL()}}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "ext"})
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
