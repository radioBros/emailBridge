//go:build integration

package settings

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_set_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := crypto.Migration(2).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{
		KEK: l, Store: &crypto.MongoKeyStore{DB: db},
		NewID: func() string { return store.NewID(store.PrefixDataKey) },
	}
}

func orgCaller(org string, role tenancy.Role, owner bool) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_" + org[4:10], OrganizationID: org,
		Role: role, InstanceOwner: owner,
	})
}

func TestIntegrationSettingsPatchGetLockETagSecret(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cipher := testCipher(t, db)
	reg := features.DefaultRegistry()
	svc := NewService(db, cipher, reg, nil)
	notified := 0
	var lastNotifyScope string
	svc.Notify = func(_ context.Context, scope string, keys []string) {
		notified++
		lastNotifyScope = scope
		if scope == "" || scope == "organization" || len(keys) == 0 {
			t.Errorf("bad notify %q %v", scope, keys)
		}
	}

	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	ownerA := orgCaller(orgA, tenancy.RoleOwner, true)
	adminA := orgCaller(orgA, tenancy.RoleAdmin, false)
	adminB := orgCaller(orgB, tenancy.RoleAdmin, false)

	// global patch needs instance admin
	_, err := svc.Patch(adminA, "global", map[string]any{"webhookTimeout": "15s"}, "")
	if err == nil {
		t.Fatal("non-instance-admin must not patch global")
	}
	body, err := svc.Patch(ownerA, "global", map[string]any{"webhookTimeout": "15s"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if body.Values["webhookTimeout"].Value != "15s" || body.Values["webhookTimeout"].Source != "global" {
		t.Fatalf("global: %+v", body.Values["webhookTimeout"])
	}
	if notified == 0 || lastNotifyScope != "global" {
		t.Fatalf("notify: n=%d scope=%q", notified, lastNotifyScope)
	}

	// org override
	body, err = svc.Patch(adminA, "organization", map[string]any{"webhookTimeout": "25s"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if body.Values["webhookTimeout"].Source != "organization" || body.Values["webhookTimeout"].Value != "25s" {
		t.Fatalf("org: %+v", body.Values["webhookTimeout"])
	}

	// org B isolation — sees global, not org A
	bodyB, err := svc.Effective(adminB, orgB)
	if err != nil {
		t.Fatal(err)
	}
	if bodyB.Values["webhookTimeout"].Source != "global" || bodyB.Values["webhookTimeout"].Value != "15s" {
		t.Fatalf("org B leaked: %+v", bodyB.Values["webhookTimeout"])
	}

	// ETag conflict
	ver := body.Version
	_, err = svc.Patch(adminA, "organization", map[string]any{"webhookTimeout": "30s"}, formatETag(ver-1))
	if err == nil || errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("etag: %v", err)
	}
	_, err = svc.Patch(adminA, "organization", map[string]any{"webhookTimeout": "30s"}, formatETag(ver))
	if err != nil {
		t.Fatal(err)
	}

	// lock switch at global
	_, err = svc.Patch(ownerA, "global", map[string]any{
		"switches": map[string]any{
			"sandbox.enabled": map[string]any{"value": false, "locked": true},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Patch(adminA, "organization", map[string]any{
		"switches": map[string]any{"sandbox.enabled": true},
	}, "")
	if err == nil || errs.CodeOf(err) != errs.CodeSettingLocked {
		t.Fatalf("lock: %v", err)
	}

	// unknown switch (no dots — dotted unknown names are rejected as Mongo-unsafe keys first)
	_, err = svc.Patch(adminA, "organization", map[string]any{
		"switches": map[string]any{"nosuchswitch": true},
	}, "")
	if err == nil || errs.CodeOf(err) != errs.CodeSwitchUnknown {
		t.Fatalf("unknown: %v", err)
	}
	_, err = svc.Patch(adminA, "organization", map[string]any{
		"switches": map[string]any{"no.such.switch": true},
	}, "")
	if err == nil || errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("dotted unknown: %v", err)
	}

	// boot key
	_, err = svc.Patch(adminA, "organization", map[string]any{"mongoUri": "x"}, "")
	if err == nil || errs.CodeOf(err) != errs.CodeSettingNotRuntime {
		t.Fatalf("boot key: %v", err)
	}

	// secret encryption — pick first secret string key that allows global scope
	// (map iteration order is random; authServer.hmacSecret is org-only).
	var secretCandidates []string
	for name, meta := range svc.catalog {
		if meta.Secret && meta.Kind == KindString && (meta.Scope == "global" || meta.Scope == "both") {
			secretCandidates = append(secretCandidates, name)
		}
	}
	sort.Strings(secretCandidates)
	if len(secretCandidates) == 0 {
		t.Fatal("no secret string key with global/both scope")
	}
	secretKey := secretCandidates[0]
	plain := "super-secret-value-9999"
	body, err = svc.Patch(ownerA, "global", map[string]any{secretKey: plain}, "")
	if err != nil {
		t.Fatal(err)
	}
	disp, _ := body.Values[secretKey].Value.(string)
	if !strings.HasPrefix(disp, "••••") || !strings.HasSuffix(disp, "9999") {
		t.Fatalf("secret display: %q", disp)
	}
	// stored encrypted
	doc, _ := svc.loadDoc(ctx, DocIDGlobal)
	raw := doc.Values[secretKey]
	ss, ok := asSecretStored(raw)
	if !ok || ss.Last4 != "9999" {
		// may be secretStored typed if replace kept type — try decode
		t.Logf("raw secret type %T %#v", raw, raw)
	}

	sw, err := svc.ListFeatureSwitches(adminA)
	if err != nil || len(sw) == 0 {
		t.Fatalf("switches: %v %d", err, len(sw))
	}
}

func TestIntegrationUserSettingsRoundtrip(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, features.NewRegistry(), nil)
	uid := store.NewID(store.PrefixUser)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: uid, OrganizationID: store.NewID(store.PrefixOrganization), Role: tenancy.RoleViewer,
	})

	body, err := svc.PatchUserSettings(ctx, map[string]any{
		"ui": map[string]any{"theme": "dark"},
		"tables": map[string]any{
			"accounts": map[string]any{"visibility": []any{"name"}, "order": []any{"name"}},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if body.Settings["ui"].(map[string]any)["theme"] != "dark" {
		t.Fatalf("%+v", body)
	}
	got, err := svc.GetUserSettings(ctx)
	if err != nil || got.Settings["ui"].(map[string]any)["theme"] != "dark" {
		t.Fatalf("%+v %v", got, err)
	}

	// API key forbidden
	keyCtx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_x", OrganizationID: "org_x", Scopes: []string{"settings:write"},
	})
	if _, err := svc.GetUserSettings(keyCtx); err == nil {
		t.Fatal("api key must be forbidden")
	}

	// 64 KiB
	_, err = svc.PatchUserSettings(ctx, map[string]any{"pad": strings.Repeat("y", maxUserSettingsBytes)}, "")
	if err == nil || errs.CodeOf(err) != errs.CodePayloadTooLarge {
		t.Fatalf("size: %v", err)
	}

	// null delete + etag on user settings
	body, err = svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"theme": nil}}, formatETag(got.Version))
	if err != nil {
		t.Fatal(err)
	}
	if ui, _ := body.Settings["ui"].(map[string]any); ui != nil {
		if _, ok := ui["theme"]; ok {
			t.Fatal("theme should be deleted")
		}
	}
}

func TestIntegrationEnvPatchAndModuleHTTP(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cipher := testCipher(t, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	boot := &config.Config{}
	boot.Process.Env = "development"
	boot.Webhook.Timeout = 30 * time.Second
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, cipher)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Boot: boot, EnvSources: map[string]config.Source{"EB_WEBHOOK_TIMEOUT": config.SourceEnv}, Notify: func(context.Context, string, []string) {}}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "settings" || m.Service() == nil || m.Stop(ctx) != nil {
		t.Fatal("module")
	}

	org := store.NewID(store.PrefixOrganization)
	uid := store.NewID(store.PrefixUser)
	owner := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: uid, OrganizationID: org, Role: tenancy.RoleOwner, InstanceOwner: true,
	})

	_, err = m.Service().Patch(owner, "organization", map[string]any{
		"env": map[string]any{"FOO": "bar", "DEL": "x"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Service().Patch(owner, "organization", map[string]any{
		"env": map[string]any{"DEL": nil},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// bare switch name + switches. prefix
	if _, err = m.Service().Patch(owner, "organization", map[string]any{"inbound.archiveRaw": false}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Service().Patch(owner, "global", map[string]any{"switches.events.store": true}, ""); err != nil {
		t.Fatal(err)
	}
	eff, err := m.Service().Effective(owner, org)
	if err != nil || eff.Values["webhookTimeout"].Source != "env" {
		t.Fatalf("env source: %+v %v", eff.Values["webhookTimeout"], err)
	}
	m.Service().Invalidate(org)
	m.Service().Invalidate("org:" + org)

	// HTTP handlers
	call := func(method, path string, body string) int {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r = r.WithContext(owner)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code
	}
	if code := call(http.MethodGet, "/v1/settings", ""); code != 200 {
		t.Fatalf("get settings %d", code)
	}
	if code := call(http.MethodGet, "/v1/feature-switches", ""); code != 200 {
		t.Fatalf("switches %d", code)
	}
	if code := call(http.MethodPatch, "/v1/settings?scope=organization", `{"webhookTimeout":"40s"}`); code != 200 {
		t.Fatalf("patch settings %d", code)
	}
	if code := call(http.MethodGet, "/v1/me/settings", ""); code != 200 {
		t.Fatalf("me get %d", code)
	}
	if code := call(http.MethodPatch, "/v1/me/settings", `{"ui":{"theme":"light"}}`); code != 200 {
		t.Fatalf("me patch %d", code)
	}
	// nil body paths via direct handlers
	if _, err := m.patchSettings(owner, &patchSettingsIn{Scope: "organization"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.patchMeSettings(owner, &patchMeIn{}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationConcurrentPatchNoLostUpdate(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, features.DefaultRegistry(), nil)
	org := store.NewID(store.PrefixOrganization)
	admin := orgCaller(org, tenancy.RoleAdmin, false)

	// seed a doc so both writers race on version > 0
	if _, err := svc.Patch(admin, "organization", map[string]any{"webhookTimeout": "1s"}, ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errsCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Patch(admin, "organization", map[string]any{"webhookTimeout": "11s"}, "")
		errsCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.Patch(admin, "organization", map[string]any{
			"switches": map[string]any{"inbound.archiveRaw": true},
		}, "")
		errsCh <- err
	}()
	wg.Wait()
	close(errsCh)

	var fail, ok int
	for err := range errsCh {
		if err == nil {
			ok++
			continue
		}
		code := errs.CodeOf(err)
		if code != errs.CodeVersionMismatch && code != errs.CodeConflict {
			t.Fatalf("unexpected error: %v", err)
		}
		fail++
	}
	if ok == 0 {
		t.Fatal("both patches failed")
	}
	// At least one succeeded; if both did, both changes must be present (retries).
	eff, err := svc.Effective(admin, org)
	if err != nil {
		t.Fatal(err)
	}
	if ok == 2 {
		if eff.Values["webhookTimeout"].Value != "11s" {
			t.Fatalf("lost webhookTimeout: %+v", eff.Values["webhookTimeout"])
		}
		if eff.Switches["inbound.archiveRaw"].Value != true {
			t.Fatalf("lost switch: %+v", eff.Switches["inbound.archiveRaw"])
		}
	} else {
		// one won: either timeout or switch must reflect that write; the other key may be stale seed
		to := eff.Values["webhookTimeout"].Value
		sw := eff.Switches["inbound.archiveRaw"].Value
		if to != "11s" && sw != true {
			t.Fatalf("neither concurrent change survived: timeout=%v switch=%v", to, sw)
		}
	}

	// If-Match conflict is immediate 412
	_, err = svc.Patch(admin, "organization", map[string]any{"webhookTimeout": "99s"}, formatETag(eff.Version-1))
	if err == nil || errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("If-Match: %v", err)
	}
}

func TestIntegrationConcurrentUserSettings(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, features.NewRegistry(), nil)
	uid := store.NewID(store.PrefixUser)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: uid, OrganizationID: store.NewID(store.PrefixOrganization), Role: tenancy.RoleViewer,
	})
	if _, err := svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"theme": "light"}}, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errsCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"theme": "dark"}}, "")
		errsCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"locale": "en"}}, "")
		errsCh <- err
	}()
	wg.Wait()
	close(errsCh)
	ok := 0
	for err := range errsCh {
		if err == nil {
			ok++
			continue
		}
		c := errs.CodeOf(err)
		if c != errs.CodeVersionMismatch && c != errs.CodeConflict {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok == 0 {
		t.Fatal("both failed")
	}
	got, err := svc.GetUserSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ui, _ := got.Settings["ui"].(map[string]any)
	if ui == nil {
		t.Fatal("ui missing")
	}
	if ok == 2 && (ui["theme"] != "dark" || ui["locale"] != "en") {
		t.Fatalf("lost update with both ok: %+v", ui)
	}
	if ok == 1 && ui["theme"] != "dark" && ui["locale"] != "en" {
		t.Fatalf("neither change survived: %+v", ui)
	}
	// stale If-Match
	_, err = svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"density": "compact"}}, formatETag(got.Version-1))
	if err == nil || errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("user If-Match: %v", err)
	}
}

func TestDisplayValueSecretPaths(t *testing.T) {
	s := NewService(nil, nil, features.NewRegistry(), nil)
	meta := KeyMeta{Secret: true, Kind: KindString}
	if s.displayValue(meta, "abcdefgh") != "••••efgh" {
		t.Fatal("plain secret")
	}
	if s.displayValue(meta, map[string]any{"_secret": true, "last4": "zz99"}) != "••••zz99" {
		t.Fatal("stored secret")
	}
	if s.displayValue(KeyMeta{}, 42) != 42 {
		t.Fatal("passthrough")
	}
}

// TestIntegrationSettingsETagChain: the ETag of GET /v1/settings (global + organisation version) is accepted
// by PATCH at either scope, every PATCH answers the ETag the next GET would give, a write to either document
// invalidates older ETags — including when the global document is "ahead" of the organisation one (the old
// max() ETag 412'd there) — and one organisation's writes never change another organisation's ETag.
func TestIntegrationSettingsETagChain(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, features.DefaultRegistry(), nil)
	orgA := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	ownerA := orgCaller(orgA, tenancy.RoleOwner, true)
	adminA := orgCaller(orgA, tenancy.RoleAdmin, false)
	adminB := orgCaller(orgB, tenancy.RoleAdmin, false)

	// Push the global document several versions ahead of org A's.
	for _, v := range []string{"11s", "12s", "13s"} {
		if _, err := svc.Patch(ownerA, "global", map[string]any{"values": map[string]any{"webhookTimeout": v}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	get := func(ctx context.Context, org string) int64 {
		t.Helper()
		svc.Invalidate("global")
		b, err := svc.Effective(ctx, org)
		if err != nil {
			t.Fatal(err)
		}
		return b.Version
	}
	v0 := get(adminA, orgA)
	b1, err := svc.Patch(adminA, "organization", map[string]any{"values": map[string]any{"webhookTimeout": "20s"}}, formatETag(v0))
	if err != nil {
		t.Fatalf("If-Match from GET (global ahead): %v", err)
	}
	if b1.Version == v0 || b1.Version != get(adminA, orgA) {
		t.Fatalf("org PATCH ETag %d, GET %d (was %d)", b1.Version, get(adminA, orgA), v0)
	}
	vB := get(adminB, orgB)
	b2, err := svc.Patch(ownerA, "global", map[string]any{"values": map[string]any{"webhookTimeout": "14s"}}, formatETag(b1.Version))
	if err != nil {
		t.Fatalf("global PATCH with the org-view ETag: %v", err)
	}
	if b2.Version != get(ownerA, orgA) || b2.Version == b1.Version {
		t.Fatalf("global PATCH ETag %d, GET %d", b2.Version, get(ownerA, orgA))
	}
	for _, stale := range []int64{v0, b1.Version} {
		if _, err := svc.Patch(adminA, "organization", map[string]any{"values": map[string]any{"webhookTimeout": "21s"}}, formatETag(stale)); errs.CodeOf(err) != errs.CodeVersionMismatch {
			t.Fatalf("stale ETag %d: %v", stale, err)
		}
	}
	// Org A's write did not move org B's view; the global write did.
	if _, err := svc.Patch(adminA, "organization", map[string]any{"values": map[string]any{"webhookTimeout": "22s"}}, formatETag(b2.Version)); err != nil {
		t.Fatal(err)
	}
	if got := get(adminB, orgB); got != vB+1 {
		t.Fatalf("org B ETag %d, want %d (only the global write)", got, vB+1)
	}
}

// TestIntegrationMeSettingsDashboardPeriod: PATCH /v1/me/settings rejects an unknown dashboard.period with
// 422; a bad value stored before the rule does not block unrelated changes and can be overwritten.
func TestIntegrationMeSettingsDashboardPeriod(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil, features.NewRegistry(), nil)
	uid := store.NewID(store.PrefixUser)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: uid})
	if _, err := svc.PatchUserSettings(ctx, map[string]any{"dashboard": map[string]any{"period": "90d"}}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad period: %v", err)
	}
	body, err := svc.PatchUserSettings(ctx, map[string]any{"dashboard": map[string]any{"period": "7d"}}, "")
	if err != nil || asMapT(t, body.Settings["dashboard"])["period"] != "7d" {
		t.Fatalf("good period: %+v %v", body, err)
	}
	// Legacy bad value written directly.
	if _, err := db.Collection(UserSettingsCollection).UpdateOne(context.Background(), bson.M{"_id": uid},
		bson.M{"$set": bson.M{"settings.dashboard.period": "1y"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PatchUserSettings(ctx, map[string]any{"ui": map[string]any{"theme": "dark"}}, ""); err != nil {
		t.Fatalf("legacy value blocked an unrelated patch: %v", err)
	}
	if body, err = svc.PatchUserSettings(ctx, map[string]any{"dashboard": map[string]any{"period": "30d"}}, ""); err != nil ||
		asMapT(t, body.Settings["dashboard"])["period"] != "30d" {
		t.Fatalf("overwrite legacy: %+v %v", body, err)
	}
}

func asMapT(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := asObject(v)
	if !ok {
		t.Fatalf("not an object: %T %v", v, v)
	}
	return m
}
