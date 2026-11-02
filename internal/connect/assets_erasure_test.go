package connect

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Upload → org erasure (Mongo brandingAssets + S3 prefix via OrgObjectCleanups).
func TestOrgErasureDeletesBrandingAssets(t *testing.T) {
	db := testMongo(t)
	ctx := context.Background()
	if err := EnsureAssetIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	_, err := db.Collection("organizations").InsertOne(ctx, bson.M{
		"_id": org, "name": "Brand Erase", "slug": "be-" + org[len(org)-6:],
		"status": "deleting", "createdAt": now, "updatedAt": now, "version": int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}

	repo := &Repo{DB: db}
	objs := newMemObjects()
	svc := NewService(repo, nil)
	svc.Assets = repo
	svc.Objects = objs
	svc.PublicURL = "https://eb.test"

	a, err := svc.UploadAsset(adminCtx(org), AssetKindLogo, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(objs.data) != 1 {
		t.Fatalf("objects: %d", len(objs.data))
	}
	n, err := db.Collection(CollectionAssets).CountDocuments(ctx, bson.M{"organizationId": org})
	if err != nil || n != 1 {
		t.Fatalf("mongo assets: %d %v", n, err)
	}

	jobID := store.NewID(store.PrefixJob)
	_, _ = db.Collection("jobs").InsertOne(ctx, bson.M{
		"_id": jobID, "organizationId": org, "kind": tenancy.OrganizationErasureKind, "state": "running",
	})

	deps := tenancy.ErasureDeps{
		DB: db,
		ListAccountIDs: func(context.Context, string) ([]string, error) {
			return nil, nil
		},
		RevokeAPIKeys: func(context.Context, string) (int64, error) { return 0, nil },
		OrgObjectCleanups: []func(context.Context, string) error{
			svc.EraseOrgObjects,
		},
	}
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	var handler tenancy.ErasureHandler
	tenancy.RegisterErasure(func(_ string, h tenancy.ErasureHandler) { handler = h }, deps)
	if _, err := handler(ctx, jobID, payload, nil, nil); err != nil {
		t.Fatal(err)
	}

	if n, _ := db.Collection(CollectionAssets).CountDocuments(ctx, bson.M{"organizationId": org}); n != 0 {
		t.Fatalf("brandingAssets remain: %d", n)
	}
	if n, _ := db.Collection("organizations").CountDocuments(ctx, bson.M{"_id": org}); n != 0 {
		t.Fatal("org doc remains")
	}
	if len(objs.data) != 0 {
		t.Fatalf("S3 objects remain: %d (asset was %s)", len(objs.data), a.ID)
	}
}
