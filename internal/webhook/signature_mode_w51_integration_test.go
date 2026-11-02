//go:build integration

package webhook

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationSignatureModeOrgWins(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_w51sig_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := settings.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := crypto.Migration(2).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{
		KEK: l, Store: &crypto.MongoKeyStore{DB: db},
		NewID: func() string { return store.NewID(store.PrefixDataKey) },
	}
	reg := features.DefaultRegistry()
	set := settings.NewService(db, cipher, reg, nil)
	org := store.NewID(store.PrefixOrganization)
	_, err = db.Collection(settings.SettingsCollection).InsertOne(ctx, settings.Doc{
		ID: settings.OrgDocID(org),
		Switches: map[string]settings.SwitchValue{
			"webhook.signature.mode": {Value: SignatureBodyOnly},
		},
		Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Settings: set}
	ep := &Endpoint{
		OrganizationID: org,
		Security:       SecurityOptions{SignatureMode: SignatureStandard},
	}
	if got := svc.signatureMode(ctx, ep); got != SignatureBodyOnly {
		t.Fatalf("org should win over default security: %s", got)
	}
	ep.Switches = map[string]any{"webhook.signature.mode": SignatureStandard}
	if got := svc.signatureMode(ctx, ep); got != SignatureStandard {
		t.Fatalf("endpoint switch should win: %s", got)
	}
	if err := ReplayedAtIndexMigration(19).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
}
