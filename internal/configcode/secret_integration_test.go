//go:build integration

package configcode

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func integrationSettings(t *testing.T) *settings.Service {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_cfgsec_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
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
	cipher := &crypto.Cipher{KEK: l, Store: &crypto.MongoKeyStore{DB: db},
		NewID: func() string { return store.NewID(store.PrefixDataKey) }}
	return settings.NewService(db, cipher, features.DefaultRegistry(), nil)
}

func adminOf(org string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_cfg", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
}

// Org A's document can only resolve org A's own settings.env secrets — never org B's, never process env.
func TestIntegrationConfigSecretsCrossTenant(t *testing.T) {
	st := integrationSettings(t)
	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	ctxA, ctxB := adminOf(orgA), adminOf(orgB)
	if _, err := st.Patch(ctxA, "organization", map[string]any{"env": map[string]any{
		"HOOK": map[string]any{"value": "fake-a-hook", "secret": true}, "PLAIN": "fake-plain",
	}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Patch(ctxB, "organization", map[string]any{"env": map[string]any{
		"HOOK":   map[string]any{"value": "fake-b-hook", "secret": true},
		"ONLY_B": map[string]any{"value": "fake-b-only", "secret": true},
	}}, ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EB_AUTH_SECRET", "fake-server-secret")

	svc := &Service{SkipFeatureGate: true, Env: st}
	in, err := svc.endpointCreate(ctxA, EndpointSpec{Name: "crm", URL: "https://crm.example.com", Secret: "${secret:HOOK}"})
	if err != nil || in.Secret != "fake-a-hook" {
		t.Fatalf("org A own secret: %q %v", in.Secret, err)
	}
	in, err = svc.endpointCreate(ctxB, EndpointSpec{Name: "crm", URL: "https://crm.example.com", Secret: "${HOOK}"})
	if err != nil || in.Secret != "fake-b-hook" {
		t.Fatalf("org B own secret: %q %v", in.Secret, err)
	}
	for _, ref := range []string{"${secret:ONLY_B}", "${secret:EB_AUTH_SECRET}", "${secret:PLAIN}"} {
		v, err := svc.resolve(ctxA, ref)
		if errs.CodeOf(err) != errs.CodeValidationFailed || v != "" {
			t.Fatalf("%s from org A: %q %v", ref, v, err)
		}
		if strings.Contains(err.Error(), "fake-") {
			t.Fatalf("error leaks a value: %v", err)
		}
	}
}
