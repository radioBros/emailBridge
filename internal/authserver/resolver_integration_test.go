//go:build integration

package authserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/settings"
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
		URI: uri, Database: fmt.Sprintf("ebtest_authsrv_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	ctx := context.Background()
	ks := &crypto.MongoKeyStore{DB: db}
	_ = db.EnsureIndexes(ctx, crypto.DataKeysCollection, ks.Indexes()...)
	key := bytes.Repeat([]byte{3}, crypto.KeySize)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
}

func TestIntegrationResolveHMACCacheNoPersist(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cipher := testCipher(t, db)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ext := "ext-1"
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": acc, "organizationId": org, "useAuthServer": true, "externalId": ext,
		"switches": bson.M{SwitchName: true},
	})
	docID := settings.OrgDocID(org)
	secretPlain := "super-secret-hmac-key"
	es, err := cipher.EncryptString(ctx, org, secretPlain, crypto.AAD{
		Collection: settings.SettingsCollection, Field: "authServer.hmacSecret", DocID: docID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-EB-Timestamp"), 10, 64)
		if !VerifySignature(secretPlain, ts, raw, r.Header.Get("X-EB-Signature")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req Request
		_ = json.Unmarshal(raw, &req)
		if req.AccountID != acc || req.Protocol != "imap" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(Response{User: "mailuser", Pass: "mailpass", ExpiresIn: 120})
	}))
	defer srv.Close()

	_, _ = db.Collection(settings.SettingsCollection).InsertOne(ctx, bson.M{
		"_id": docID,
		"values": bson.M{
			"authServer.url": srv.URL,
			"authServer.hmacSecret": bson.M{
				"_secret": true, "last4": "y-key", "kid": es.KID, "dek": es.DEK, "nonce": es.Nonce, "ct": es.CT,
			},
			"authServer.ttl":     "5m",
			"authServer.timeout": "2s",
		},
		"switches": bson.M{SwitchName: bson.M{"value": true}},
	})

	r := NewResolver(db, cipher, nil, nil)
	r.HTTP = srv.Client()
	u, p, tok, err := r.Resolve(ctx, acc, "imap")
	if err != nil || u != "mailuser" || p != "mailpass" || tok != "" {
		t.Fatalf("%v %s %s %s", err, u, p, tok)
	}
	// second call hits cache
	_, _, _, err = r.Resolve(ctx, acc, "imap")
	if err != nil || hits != 1 {
		t.Fatalf("cache: hits=%d err=%v", hits, err)
	}
	// credentials must not appear in the account document
	var stored bson.M
	if err := db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": acc}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(stored)
	if bytes.Contains(raw, []byte("mailpass")) {
		t.Fatal("password persisted")
	}

	// tampered secret → decrypt may still work but signature fails at server
	bad := NewResolver(db, cipher, nil, nil)
	bad.HTTP = srv.Client()
	// force miss by different protocol
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv2.Close()
	_, _ = db.Collection(settings.SettingsCollection).UpdateOne(ctx, bson.M{"_id": docID}, bson.M{
		"$set": bson.M{"values.authServer.url": srv2.URL},
	})
	bad.cache = newMemCache()
	if _, _, _, err := bad.Resolve(ctx, acc, "smtp"); errs.CodeOf(err) != errs.CodeAuthServerFailed {
		t.Fatalf("want failed, got %v", err)
	}
}
