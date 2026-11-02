//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
)

// testDB connects to EB_TEST_MONGO_URI (a replica set). P0.26 replaces this with testcontainers helpers.
func testDB(t *testing.T) *DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	name := fmt.Sprintf("ebtest_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Connect(ctx, Options{URI: uri, Database: name, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB replica set at %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	return db
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestIntegrationWithTxCommitAndAbort(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := db.Collection("txtest")
	// create the collection outside a transaction first
	_, _ = c.InsertOne(ctx, bson.M{"_id": "seed"})
	err := db.WithTx(ctx, func(sc context.Context) error {
		_, err := c.InsertOne(sc, bson.M{"_id": "a"})
		return err
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	boom := errors.New("boom")
	err = db.WithTx(ctx, func(sc context.Context) error {
		if _, err := c.InsertOne(sc, bson.M{"_id": "b"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("abort error = %v", err)
	}
	n, _ := c.CountDocuments(ctx, bson.M{"_id": bson.M{"$in": bson.A{"a", "b"}}})
	if n != 1 {
		t.Fatalf("want only a committed, got %d docs", n)
	}
}

func TestIntegrationLock(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	l1, err := db.TryLock(ctx, "x", "n1", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TryLock(ctx, "x", "n2", 3*time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second holder: %v", err)
	}
	// renewals keep it held beyond the TTL
	time.Sleep(4 * time.Second)
	if _, err := db.TryLock(ctx, "x", "n2", 3*time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("lock expired despite renewal: %v", err)
	}
	if err := l1.Release(ctx); err != nil {
		t.Fatal(err)
	}
	l2, err := db.TryLock(ctx, "x", "n2", 3*time.Second)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	// steal after forced expiry → l2 sees Lost
	_, err = db.Collection(LocksCollection).UpdateOne(ctx, bson.M{"_id": "x"}, bson.M{"$set": bson.M{"expiresAt": time.Now().Add(-time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TryLock(ctx, "x", "n3", 3*time.Second); err != nil {
		t.Fatalf("steal: %v", err)
	}
	select {
	case <-l2.Lost():
	case <-time.After(3 * time.Second):
		t.Fatal("l2 not marked lost")
	}
	if _, err := db.TryLock(ctx, "short", "n1", time.Second); err == nil {
		t.Fatal("too-short ttl must fail")
	}
}

func TestIntegrationMigrationsConcurrentNodes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	var runs atomic.Int32
	migs := []Migration{
		{Number: 1, Name: "accounts indexes", Up: func(ctx context.Context, d *DB) error {
			runs.Add(1)
			time.Sleep(300 * time.Millisecond)
			return d.EnsureIndexes(ctx, "accounts", mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}})
		}},
		{Number: 2, Name: "events ttl", Up: func(ctx context.Context, d *DB) error {
			runs.Add(1)
			return d.EnsureIndexes(ctx, "events", mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)})
		}},
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := &Migrator{DB: db, Holder: fmt.Sprintf("node%d", i), Migrations: migs, Log: quietLog(), PollInterval: 100 * time.Millisecond, LockTTL: 6 * time.Second}
			errs[i] = m.Run(ctx)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("node%d: %v", i, err)
		}
	}
	if runs.Load() != 2 {
		t.Fatalf("each migration must run exactly once, ran %d steps", runs.Load())
	}
	st, err := (&Migrator{DB: db, Migrations: migs}).Status(ctx)
	if err != nil || len(st.Pending) != 0 || len(st.Applied) != 2 || st.Known != 2 {
		t.Fatalf("status = %+v %v", st, err)
	}
	// older binary (knows only migration 1) refuses to start
	old := &Migrator{DB: db, Holder: "old", Migrations: migs[:1], Log: quietLog()}
	if err := old.Run(ctx); err == nil {
		t.Fatal("older binary must refuse")
	}
	old.AllowOlderBinary = true
	if err := old.Run(ctx); err != nil {
		t.Fatalf("AllowOlderBinary: %v", err)
	}
	// renamed migration is detected
	renamed := []Migration{{Number: 1, Name: "renamed", Up: noop}, migs[1]}
	if _, err := (&Migrator{DB: db, Migrations: renamed}).Status(ctx); err == nil {
		t.Fatal("renamed migration must be detected")
	}
}

func TestIntegrationModuleProvidesDB(t *testing.T) {
	db := testDB(t)
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: quietLog()})
	m := &Module{Options: Options{URI: uri, Database: db.DB.Name()}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	got, err := app.MustGet[*DB](a)
	if err != nil || got == nil {
		t.Fatalf("not provided: %v", err)
	}
	if err := m.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
}
