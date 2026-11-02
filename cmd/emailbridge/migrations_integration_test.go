//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/test/containers"
)

// Every migration of the binary applies in order on a fresh database, and a second run is a no-op. Package
// tests only run their own migrations, so a clash between packages (two indexes with the same name and
// different keys on one collection) shows up here in seconds instead of at the first node boot.
func TestAllMigrationsApplyOnAFreshDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Connect(ctx, store.Options{URI: containers.MongoShared(t), Database: containers.Database(t), ConnectTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(context.Background()); _ = db.Close(context.Background()) })
	all := migrations()
	for i, m := range all {
		if m.Number != i+1 {
			t.Fatalf("migration %q has number %d at position %d: numbers must be 1..n in order", m.Name, m.Number, i+1)
		}
	}
	for run := 1; run <= 2; run++ {
		mg := &store.Migrator{DB: db, Holder: "test", Migrations: all}
		if err := mg.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		st, err := mg.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Pending) != 0 || len(st.Applied) != len(all) {
			t.Fatalf("run %d: applied %v, pending %v", run, st.Applied, st.Pending)
		}
	}
}
