//go:build integration

package webhook

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationNeedsUnion(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_needs_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	defer func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) }()
	org, other := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	_, _ = db.Collection(EndpointsCollection).InsertMany(ctx, []any{
		bson.M{"_id": "e1", "organizationId": org, "enabled": true, "payload": bson.M{"headers": []string{"Subject", "X-Custom"}, "text": "plain", "textMax": 100}},
		bson.M{"_id": "e2", "organizationId": org, "enabled": false, "payload": bson.M{"attachments": true}},
		bson.M{"_id": "e3", "organizationId": other, "enabled": true, "payload": bson.M{"headers": []string{"*"}}},
	})
	_, _ = db.Collection(routesCollection).InsertMany(ctx, []any{
		bson.M{"_id": "r1", "organizationId": org, "enabled": true, "payload": bson.M{"headers": []string{"message-id"}, "attachments": true, "textMax": 500, "text": "both"}},
	})
	r := &NeedsResolver{DB: db}
	n, err := r.Needs(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.Headers, []string{"message-id", "subject", "x-custom"}) || !n.Text || n.TextMax != 500 || !n.Attachments {
		t.Fatalf("needs %+v", n)
	}
	if n2, _ := r.Needs(ctx, other); !slices.Equal(n2.Headers, []string{"*"}) || n2.Text {
		t.Fatalf("other %+v", n2)
	}
	if n3, _ := r.Needs(ctx, store.NewID(store.PrefixOrganization)); len(n3.Headers) != 0 || n3.Text || n3.Attachments {
		t.Fatalf("empty %+v", n3)
	}
	// cached for TTL
	_, _ = db.Collection(EndpointsCollection).DeleteMany(ctx, bson.M{"organizationId": org})
	if n4, _ := r.Needs(ctx, org); !n4.Text {
		t.Fatal("cache")
	}
}
