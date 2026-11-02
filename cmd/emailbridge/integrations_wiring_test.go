//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/integrations"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestIfMatchETag(t *testing.T) {
	if ifMatchETag(0) != "" || ifMatchETag(-1) != "" {
		t.Fatal("zero/negative must skip If-Match")
	}
	if ifMatchETag(3) != `"v3"` {
		t.Fatalf("got %q", ifMatchETag(3))
	}
}

type wiringHarness struct {
	db       *store.DB
	cipher   *crypto.Cipher
	webhook  *webhook.Service
	rules    *rules.Repo
	settings *settings.Service
	orgID    string
	ctx      context.Context
}

func newWiringHarness(t *testing.T) *wiringHarness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_intwire_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	for _, migration := range []store.Migration{
		webhook.Migration(1), events.Migration(1), rules.Migration(1), audit.Migration(1),
		settings.Migration(1), crypto.Migration(1), integrations.Migration(35),
	} {
		if err := migration.Up(root, db); err != nil {
			t.Fatal(err)
		}
	}
	keyStore := &crypto.MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(root, crypto.DataKeysCollection, keyStore.Indexes()...); err != nil {
		t.Fatal(err)
	}
	local, err := crypto.NewLocal([]crypto.LocalKey{{ID: "test", Key: bytes.Repeat([]byte{7}, crypto.KeySize)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{
		KEK: local, Store: keyStore, NewID: func() string { return store.NewID(store.PrefixDataKey) },
	}
	engine, err := rules.NewEngine(rules.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ruleRepo := &rules.Repo{DB: db, Engine: engine, Cipher: cipher}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	orgID := store.NewID(store.PrefixOrganization)
	ctx := store.WithOrganization(root, orgID)
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: orgID, Role: tenancy.RoleOwner,
	})
	wh := &webhook.Service{
		Repo: &webhook.Repo{DB: db}, Cipher: cipher, Egress: egress.NewDialer(egress.Policy{}),
		Events: &events.Writer{DB: db}, Rules: ruleRepo, Engine: engine,
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: 24 * time.Hour},
		Log:   quiet, SecurityLog: quiet, DeliveryPartitions: 4, Concurrency: 4,
		Retention: 24 * time.Hour, RotationOverlap: time.Hour, RandFloat: func() float64 { return .5 },
		Now: func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) },
	}
	sts := settings.NewService(db, cipher, features.DefaultRegistry(), nil)
	return &wiringHarness{
		db: db, cipher: cipher, webhook: wh, rules: ruleRepo, settings: sts, orgID: orgID, ctx: ctx,
	}
}

func (h *wiringHarness) adapters() (
	integrations.EndpointResources, integrations.RouteResources, integrations.SettingsResources,
) {
	return &webhookEndpointAdapter{svc: h.webhook},
		&rulesRouteAdapter{repo: h.rules},
		&settingsAdapter{svc: h.settings}
}

func TestWiringSlackLifecycle(t *testing.T) {
	h := newWiringHarness(t)
	eps, rts, sts := h.adapters()

	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer hookSrv.Close()

	svc := &integrations.Service{
		Repo: &integrations.Repo{DB: h.db}, Registry: integrations.DefaultRegistry(), Cipher: h.cipher,
		Endpoints: eps, Routes: rts, Settings: sts, HTTP: hookSrv.Client(),
	}

	v, _, err := svc.Install(h.ctx, integrations.TypeSlack, integrations.InstallInput{
		Config: map[string]any{"webhookUrl": hookSrv.URL, "channelHint": "#alerts"},
	})
	if err != nil || v == nil {
		t.Fatalf("install: %v %+v", err, v)
	}
	if len(v.CreatedResources.Endpoints) != 1 || len(v.CreatedResources.Routes) == 0 { // one route per notified event type
		t.Fatalf("resources: %+v", v.CreatedResources)
	}
	epID, rtID := v.CreatedResources.Endpoints[0], v.CreatedResources.Routes[0]

	ep, err := h.webhook.GetEndpoint(h.ctx, epID)
	if err != nil || !ep.Enabled || ep.URL != hookSrv.URL {
		t.Fatalf("endpoint: %+v %v", ep, err)
	}
	rt, err := h.rules.Get(h.ctx, rtID)
	if err != nil || !rt.Enabled || rt.EndpointID != epID {
		t.Fatalf("route: %+v %v", rt, err)
	}
	body, err := h.settings.Effective(h.ctx, h.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if sw, ok := body.Switches[integrations.SwitchName(integrations.TypeSlack)]; !ok || sw.Value != true {
		t.Fatalf("switch: %+v", body.Switches[integrations.SwitchName(integrations.TypeSlack)])
	}

	off := false
	patched, err := svc.Patch(h.ctx, v.ID, fmt.Sprintf(`"v%d"`, v.Version), integrations.PatchInput{Enabled: &off})
	if err != nil || patched.Enabled {
		t.Fatalf("disable: %+v %v", patched, err)
	}
	ep, err = h.webhook.GetEndpoint(h.ctx, epID)
	if err != nil || ep.Enabled {
		t.Fatalf("endpoint still enabled: %+v %v", ep, err)
	}
	rt, err = h.rules.Get(h.ctx, rtID)
	if err != nil || rt.Enabled {
		t.Fatalf("route still enabled: %+v %v", rt, err)
	}

	if err := svc.Uninstall(h.ctx, patched.ID, fmt.Sprintf(`"v%d"`, patched.Version)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.webhook.GetEndpoint(h.ctx, epID); err == nil {
		t.Fatal("endpoint should be gone")
	}
	if _, err := h.rules.Get(h.ctx, rtID); err == nil {
		t.Fatal("route should be gone")
	}
	body, err = h.settings.Effective(h.ctx, h.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if sw := body.Switches[integrations.SwitchName(integrations.TypeSlack)]; sw.Value == true {
		t.Fatalf("switch still on: %+v", sw)
	}
}

func TestWiringRspamdSettings(t *testing.T) {
	h := newWiringHarness(t)
	eps, rts, sts := h.adapters()

	ping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ping.Close()

	svc := &integrations.Service{
		Repo: &integrations.Repo{DB: h.db}, Registry: integrations.DefaultRegistry(), Cipher: h.cipher,
		Endpoints: eps, Routes: rts, Settings: sts, HTTP: ping.Client(),
	}

	rz, _, err := svc.Install(h.ctx, integrations.TypeRspamd, integrations.InstallInput{
		Name:    "Rspamd main",
		Config:  map[string]any{"baseUrl": ping.URL, "timeout": "3s"},
		Secrets: map[string]string{"password": "secret"},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := h.settings.Effective(h.ctx, h.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if body.Values["integrations.rspamd.baseUrl"].Value != ping.URL {
		t.Fatalf("baseUrl: %+v", body.Values["integrations.rspamd.baseUrl"])
	}
	if body.Values["integrations.rspamd.timeout"].Value != "3s" {
		t.Fatalf("timeout: %+v", body.Values["integrations.rspamd.timeout"])
	}
	if len(rz.CreatedResources.Settings) < 2 {
		t.Fatalf("owned settings: %+v", rz.CreatedResources.Settings)
	}

	if err := svc.Uninstall(h.ctx, rz.ID, fmt.Sprintf(`"v%d"`, rz.Version)); err != nil {
		t.Fatal(err)
	}
	body, err = h.settings.Effective(h.ctx, h.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if body.Values["integrations.rspamd.baseUrl"].Source == "organization" {
		t.Fatalf("baseUrl not cleared: %+v", body.Values["integrations.rspamd.baseUrl"])
	}
	if body.Values["integrations.rspamd.timeout"].Source == "organization" {
		t.Fatalf("timeout not cleared: %+v", body.Values["integrations.rspamd.timeout"])
	}
}
