//go:build integration

package archive

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
)

// When the sync engine deletes the index entry of an archived message (it was expunged), a tombstone keeps
// the archived source downloadable; retention later deletes both the object and the tombstone.
func TestIntegrationArchiveTombstone(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-48 * time.Hour)
	body := rawMsg("tomb", "<tomb@x>")
	msgID := store.NewID(store.PrefixMessage)
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, old, body)
	if err != nil {
		t.Fatal(err)
	}
	seeded := h.seedIndexed(t, 7, 1, key, old)
	if _, err := h.db.Collection(index.Messages).UpdateOne(ctx, bson.M{"_id": seeded}, bson.M{"$set": bson.M{"messageIdHeader": "<tomb@x>"}}); err != nil {
		t.Fatal(err)
	}
	plain := h.seedIndexed(t, 8, 1, "", old) // no archive: no tombstone

	ix := &index.Index{DB: h.db}
	if err := h.db.WithTx(ctx, func(tx context.Context) error { return ix.DeleteMessages(tx, []string{seeded, plain}) }); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteMessages(ctx, []string{seeded}); err != nil { // repeat: idempotent
		t.Fatal(err)
	}
	if n, _ := h.db.Collection(index.Messages).CountDocuments(ctx, bson.M{"_id": bson.M{"$in": []string{seeded, plain}}}); n != 0 {
		t.Fatalf("index entries left: %d", n)
	}
	if n, _ := h.db.Collection(index.ArchivedMessages).CountDocuments(ctx, bson.M{}); n != 1 {
		t.Fatalf("tombstones: %d, want 1", n)
	}

	for name, resolve := range map[string]func(context.Context, string, string) (*rawResult, error){
		"archive": h.svc.resolveArchive, "raw": h.svc.resolveRaw,
	} {
		res, err := resolve(h.ctx, h.accID, seeded)
		if err != nil {
			t.Fatalf("%s after the index entry is gone: %v", name, err)
		}
		data, _ := io.ReadAll(res.Reader)
		_ = res.Reader.Close()
		if res.Source != SourceArchive || !bytes.Contains(data, []byte("Subject: tomb")) {
			t.Fatalf("%s: source %s body %q", name, res.Source, data)
		}
	}
	if _, err := h.svc.resolveArchive(h.ctx, h.accID, plain); err == nil {
		t.Fatal("a message that was never archived has nothing to serve")
	}

	if _, err := h.db.Collection("accounts").InsertOne(ctx, bson.M{"_id": h.accID, "organizationId": h.org, "archiveRetention": "24h"}); err != nil {
		t.Fatal(err)
	}
	h.svc.SweepRetention(ctx)
	if n, _ := h.db.Collection(index.ArchivedMessages).CountDocuments(ctx, bson.M{}); n != 0 {
		t.Fatalf("expired tombstones left: %d", n)
	}
	if rc, _, err := h.store.Open(ctx, key); err == nil {
		_ = rc.Close()
		t.Fatal("retention must delete the archived object of a tombstone")
	}
}
