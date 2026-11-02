//go:build integration

package pecreceipts

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationRepo(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_pecrcp_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(41).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	r := &Repo{DB: db}
	org, acc := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixAccount)
	out := store.NewID(store.PrefixOutbound)
	if _, err := db.Collection("outboundMessages").InsertOne(ctx, bson.M{"_id": out, "organizationId": org, "accountId": acc, "messageIdHeader": "<m1@x>"}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"<m1@x>", "m1@x"} {
		if id, err := r.LinkOutbound(ctx, org, acc, ref); err != nil || id != out {
			t.Fatalf("link %s: %q %v", ref, id, err)
		}
	}
	if id, _ := r.LinkOutbound(ctx, org, store.NewID(store.PrefixAccount), "<m1@x>"); id != "" {
		t.Fatal("another account must not link")
	}
	if id, _ := r.LinkOutbound(ctx, org, acc, " "); id != "" {
		t.Fatal("empty reference")
	}

	rc := &Receipt{ID: store.NewID(store.PrefixPECReceipt), OrganizationID: org, AccountID: acc, OutboundID: &out,
		Type: pec.ReceiptDelivery, TransportID: "t1", DeliveredTo: "a@pec.test", At: time.Now().UTC().Truncate(time.Millisecond)}
	err = db.WithTx(ctx, func(tx context.Context) error {
		if err := r.Insert(tx, rc); err != nil {
			return err
		}
		dup := *rc
		dup.ID = store.NewID(store.PrefixPECReceipt)
		return r.Insert(tx, &dup) // same unique key inside the same transaction: a no-op, not an abort
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Exists(ctx, org, acc, "t1", pec.ReceiptDelivery, "a@pec.test"); !ok {
		t.Fatal("exists")
	}
	got, err := r.Get(ctx, org, rc.ID)
	if err != nil || got.TransportID != "t1" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := r.Get(ctx, store.NewID(store.PrefixOrganization), rc.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("other org: %v", err)
	}
	var ob struct {
		PEC OutboundPECStatus `bson:"pec"`
	}
	_ = db.Collection("outboundMessages").FindOne(ctx, bson.M{"_id": out}).Decode(&ob)
	if ob.PEC.State != StateDelivered || len(ob.PEC.Receipts) != 1 {
		t.Fatalf("outbound: %+v", ob.PEC)
	}
	n, _ := db.Collection(Collection).CountDocuments(ctx, bson.M{})
	if n != 1 {
		t.Fatalf("rows %d", n)
	}
}
