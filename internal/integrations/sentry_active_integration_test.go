//go:build integration

package integrations

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationActiveSentry(t *testing.T) {
	db := testDB(t)
	// Other tests in the package may leave sentry rows: this test owns a fresh collection state.
	_, _ = db.Collection(Collection).DeleteMany(context.Background(), map[string]any{"type": connectors.TypeSentry})
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: testCipher(t, db),
		Endpoints: newFakeEndpoints(), Routes: newFakeRoutes(), Settings: newFakeSettings(), limiter: newCallbackLimiter(60),
	}
	if rt, err := svc.ActiveSentry(context.Background()); err != nil || rt.Enabled {
		t.Fatalf("none installed: %+v %v", rt, err)
	}
	org := store.NewID(store.PrefixOrganization)
	owner := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_owner", OrganizationID: org, Role: tenancy.RoleOwner, InstanceOwner: true,
	})
	v, _, err := svc.Install(owner, connectors.TypeSentry, InstallInput{
		Config:  map[string]any{"environment": "staging"},
		Secrets: map[string]string{"dsn": "https://key@sentry.example.test/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := svc.ActiveSentry(context.Background())
	if err != nil || !rt.Enabled || rt.DSN != "https://key@sentry.example.test/1" || rt.Environment != "staging" {
		t.Fatalf("active: %+v %v", rt, err)
	}
	off := false
	if _, err := svc.Patch(owner, v.ID, "", PatchInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if rt, err := svc.ActiveSentry(context.Background()); err != nil || rt.Enabled {
		t.Fatalf("disabled: %+v %v", rt, err)
	}
}
