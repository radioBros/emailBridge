//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/gateways"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/suppression"
)

// Provider delivery webhooks (P8.3) end like sync-detected bounces: hard bounces and complaints suppress,
// soft bounces do not, and the events carry the outbound message's account.
func TestIntegrationGatewaySink(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_gwsink_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	for _, m := range []store.Migration{events.Migration(1), suppression.Migration(29)} {
		if err := m.Up(root, db); err != nil {
			t.Fatal(err)
		}
	}
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	out := store.NewID(store.PrefixOutbound)
	ext := "crm-42"
	if _, err := db.Collection("outboundMessages").InsertOne(root, bson.M{"_id": out, "organizationId": org, "accountId": acc}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("accounts").InsertOne(root, bson.M{"_id": acc, "organizationId": org, "externalId": ext}); err != nil {
		t.Fatal(err)
	}
	sup := &suppression.Service{Repo: &suppression.Repo{DB: db}}
	sink := gatewaySink{events: &events.Writer{DB: db}, suppress: sup, accountOf: outboundAccount(db)}

	err = sink.Deliver(root, org, "gwy_x", []gateways.DeliveryEvent{
		{Type: gateways.DeliveryBounced, BounceType: "hard", Recipient: "gone@example.test", OutboundID: out, Diagnostic: "550 5.1.1"},
		{Type: gateways.DeliveryBounced, BounceType: "soft", Recipient: "full@example.test", OutboundID: out},
		{Type: gateways.DeliveryComplained, Recipient: "angry@example.test", OutboundID: out},
		{Type: gateways.DeliverySent, Recipient: "ok@example.test", OutboundID: out},
	})
	if err != nil {
		t.Fatal(err)
	}
	octx := store.WithOrganization(root, org)
	if err := sup.CheckAddresses(octx, org, []string{"gone@example.test"}, ""); errs.CodeOf(err) == "" {
		t.Fatal("hard bounce must suppress")
	}
	if err := sup.CheckAddresses(octx, org, []string{"angry@example.test"}, ""); errs.CodeOf(err) == "" {
		t.Fatal("complaint must suppress")
	}
	if err := sup.CheckAddresses(octx, org, []string{"full@example.test"}, ""); err != nil {
		t.Fatalf("soft bounce must not suppress: %v", err)
	}
	n, err := db.Collection("events").CountDocuments(root, bson.M{"organizationId": org, "accountId": acc,
		"type": bson.M{"$in": []string{events.MessageBounced, events.MessageComplained}}})
	if err != nil || n != 3 {
		t.Fatalf("events with the account: %d %v", n, err)
	}
	if a, e := outboundAccount(db)(root, org, out); a != acc || e == nil || *e != ext {
		t.Fatalf("account of outbound: %s %v", a, e)
	}
	if a, _ := outboundAccount(db)(root, store.NewID(store.PrefixOrganization), out); a != "" {
		t.Fatal("another organisation must not resolve the outbound message")
	}
}
