//go:build integration

package crypto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationMongoKeyStore(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_crypto_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	defer func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) }()
	ks := &MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(ctx, DataKeysCollection, ks.Indexes()...); err != nil {
		t.Fatal(err)
	}
	l, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}}, "")
	newID := func() string { return store.NewID(store.PrefixDataKey) }
	// many nodes encrypting for the same new organisation at once → one active DEK
	var wg sync.WaitGroup
	deks := make([]string, 8)
	for i := range deks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &Cipher{KEK: l, Store: ks, NewID: newID}
			es, err := c.EncryptString(ctx, "org_race", "v", aad)
			if err != nil {
				t.Error(err)
				return
			}
			deks[i] = es.DEK
		}()
	}
	wg.Wait()
	for _, d := range deks {
		if d != deks[0] {
			t.Fatalf("concurrent creation produced several DEKs: %v", deks)
		}
	}
	c := &Cipher{KEK: l, Store: ks, NewID: newID}
	es, _ := c.EncryptString(ctx, "", "instance secret", aad)
	if v, err := c.DecryptString(ctx, es, aad); err != nil || v != "instance secret" {
		t.Fatalf("instance-level: %v", err)
	}
	if err := c.RotateDEK(ctx, "org_race"); err != nil {
		t.Fatal(err)
	}
	es2, _ := c.EncryptString(ctx, "org_race", "v", aad)
	if es2.DEK == deks[0] {
		t.Fatal("rotation must create a new DEK")
	}
	all, _ := ks.All(ctx)
	if len(all) != 3 {
		t.Fatalf("keys = %d", len(all))
	}
	if _, err := ks.Get(ctx, "dek_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if n, err := c.RotateKEK(ctx); err != nil || n != 0 {
		t.Fatalf("rotate with same kek: %d %v", n, err)
	}
}

func TestIntegrationMongoKeyStoreErrorsAndRewrap(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_crypto2_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	defer func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) }()
	ks := &MongoKeyStore{DB: db}
	l1, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}, {ID: "k2", Key: key(2)}}, "k1")
	c := &Cipher{KEK: l1, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
	es, _ := c.EncryptString(ctx, "org_x", "v", aad)
	l2, _ := NewLocal([]LocalKey{{ID: "k1", Key: key(1)}, {ID: "k2", Key: key(2)}}, "k2")
	c.KEK, c.cache = l2, nil
	if n, err := c.RotateKEK(ctx); err != nil || n != 1 {
		t.Fatalf("rewrap: %d %v", n, err)
	}
	dk, _ := ks.Get(ctx, es.DEK)
	if dk.KEKID != "local:k2" {
		t.Fatalf("kek id = %s", dk.KEKID)
	}
	// errors with a cancelled context
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ks.Active(cctx, "org_x"); err == nil {
		t.Error("Active must fail")
	}
	if _, err := ks.Get(cctx, es.DEK); err == nil || errors.Is(err, ErrNotFound) {
		t.Error("Get must fail")
	}
	if _, err := ks.Insert(cctx, DataKey{ID: "dek_x", Status: DEKActive}); err == nil {
		t.Error("Insert must fail")
	}
	if _, err := ks.All(cctx); err == nil {
		t.Error("All must fail")
	}
}
