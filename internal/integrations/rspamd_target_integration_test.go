//go:build integration

package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationRspamdTarget(t *testing.T) {
	db := testDB(t)
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: testCipher(t, db),
		Endpoints: newFakeEndpoints(), Routes: newFakeRoutes(), Settings: newFakeSettings(), limiter: newCallbackLimiter(60),
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := context.Background()

	if opts, err := svc.RspamdTarget(ctx, org); err != nil || opts != nil {
		t.Fatalf("no instance: %+v %v", opts, err)
	}

	var gotPassword string
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPassword = r.Header.Get("Password")
		_ = json.NewEncoder(w).Encode(map[string]any{"score": 7.5, "action": "add header", "symbols": map[string]any{"BAYES_SPAM": map[string]any{"score": 5.1}}})
	}))
	defer rs.Close()

	if _, _, err := svc.Install(orgCtx(org, tenancy.RoleOwner), TypeRspamd, InstallInput{
		Config:  map[string]any{"baseUrl": rs.URL, "timeout": "2s"},
		Secrets: map[string]string{"password": "s3cret"},
	}); err != nil {
		t.Fatal(err)
	}
	// The empty answer above is cached; a fresh node (empty cache) sees the new instance.
	svc.rspamd = rspamdCache{}
	opts, err := svc.RspamdTarget(ctx, org)
	if err != nil || opts == nil || opts.BaseURL != rs.URL || opts.Password != "s3cret" || opts.Timeout != 2*time.Second {
		t.Fatalf("target: %+v %v", opts, err)
	}
	res, err := connectors.RspamdCheck(ctx, svc.HTTPClient(), *opts, []byte("Subject: x\r\n\r\nbody"))
	if err != nil || res.Score != 7.5 || gotPassword != "s3cret" {
		t.Fatalf("check: %+v %v password=%q", res, err, gotPassword)
	}

	// Another organisation never sees this instance.
	if other, err := svc.RspamdTarget(ctx, store.NewID(store.PrefixOrganization)); err != nil || other != nil {
		t.Fatalf("other org: %+v %v", other, err)
	}
	// Cached: a second call does not need the database.
	if again, _ := svc.RspamdTarget(ctx, org); again != opts {
		t.Fatal("expected the cached target")
	}
}
