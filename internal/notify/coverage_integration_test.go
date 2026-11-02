//go:build integration

package notify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationServiceResolveMarkAllAndAPI(t *testing.T) {
	db, eng, svc, _, _, _ := harness(t)
	org := store.NewID(store.PrefixOrganization)
	user := store.NewID(store.PrefixUser)
	n, err := eng.RaiseSync(context.Background(), Problem{
		Scope: org, Key: "k1", Category: CatWebhooks, Severity: SeverityInfo,
		TitleKey: "t", DetailKey: "d", Link: "/webhooks/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := ctxUser(org, user, tenancy.RoleOwner, false)
	if err := svc.MarkAllRead(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Resolve(ctx, n.ID)
	if err != nil || got.State != StateResolved {
		t.Fatalf("%+v %v", got, err)
	}
	// reopen then GetByID
	n2, err := eng.RaiseSync(context.Background(), Problem{
		Scope: org, Key: "k1", Category: CatWebhooks, Severity: SeverityWarning,
		TitleKey: "t", DetailKey: "d",
	})
	if err != nil || n2.State != StateOpen {
		t.Fatalf("reopen %+v %v", n2, err)
	}
	g, err := eng.Repo.GetByID(context.Background(), n2.ID)
	if err != nil || g.ID != n2.ID {
		t.Fatal(err)
	}
	// sorts + unread filter
	unread := true
	_, _ = svc.List(ctx, ListFilter{Sort: "firstSeen", Unread: &unread, Page: 1, Limit: 10})
	_, _ = svc.List(ctx, ListFilter{Sort: "-severity", Category: CatWebhooks})
	_, _ = svc.List(ctx, ListFilter{Sort: "severity", Severity: SeverityWarning, IncludeTotal: true})
	_, _ = svc.List(ctx, ListFilter{Sort: "-firstSeen"})
	_, _ = svc.List(ctx, ListFilter{Sort: "lastSeen"})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := httpapi.New(httpapi.Options{AccessLog: log, SecurityLog: log})
	if err != nil {
		t.Fatal(err)
	}
	mod := &Module{Log: log, Env: "test", svc: svc, engine: eng}
	mod.registerAPI(srv)
	if mod.Name() != "notify" || len(mod.Roles()) != 0 {
		t.Fatal("module meta")
	}
	if mod.Engine() != eng || mod.Service() != svc {
		t.Fatal("accessors")
	}
	_ = mod.Metrics()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Router.ServeHTTP(rr, req)
	_ = rr.Code

	// Call handlers directly (huma already registered).
	_, _ = mod.list(ctx, &listIn{Unread: "true", Sort: "-lastSeen"})
	_, _ = mod.list(ctx, &listIn{Unread: "false"})
	_, _ = mod.summary(ctx, &struct{}{})
	in := &markReadIn{}
	in.Body.IDs = []string{n2.ID}
	_, _ = mod.markRead(ctx, in)
	_, _ = mod.markAllRead(ctx, &struct{}{})
	n3, _ := eng.RaiseSync(context.Background(), Problem{
		Scope: org, Key: "k-api", Category: CatLimits, Severity: SeverityInfo, TitleKey: "t", DetailKey: "d",
	})
	_, _ = mod.resolve(ctx, &resolveIn{NotificationID: n3.ID})
	_ = (&Channels{}).log()
	_ = db
}

func TestIntegrationWatchersAll(t *testing.T) {
	db, eng, _, _, _, _ := harness(t)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ep := store.NewID(store.PrefixEndpoint)
	keyID := store.NewID(store.PrefixAPIKey)
	intID := store.NewID(store.PrefixIntegration)
	now := time.Now().UTC()

	_, _ = db.Collection("webhookEndpoints").InsertOne(context.Background(), bson.M{
		"_id": ep, "organizationId": org, "name": "E", "enabled": true,
		"breaker": bson.M{"state": "open"}, "failureStartedAt": now.Add(-30 * time.Minute),
	})
	_, _ = db.Collection("deliveries").InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixDelivery), "endpointId": ep, "organizationId": org,
		"state": "deadLettered", "updatedAt": now,
	})
	for i := 0; i < 12; i++ {
		_, _ = db.Collection("deliveries").InsertOne(context.Background(), bson.M{
			"_id": store.NewID(store.PrefixDelivery), "endpointId": ep, "organizationId": org,
			"state": "deadLettered", "updatedAt": now,
		})
	}
	_, _ = db.Collection("accounts").InsertOne(context.Background(), bson.M{
		"_id": acc, "organizationId": org, "name": "A", "email": "a@t", "state": "authError",
		"lastSyncAt": now.Add(-5 * time.Hour), "certExpiresAt": now.Add(48 * time.Hour),
	})
	_, _ = db.Collection("accounts").InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixAccount), "organizationId": org, "name": "B", "email": "b@t",
		"state": "connectError", "disabled": true,
	})
	_, _ = db.Collection("outboundMessages").InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixOutbound), "organizationId": org, "accountId": acc,
		"state": "failed", "updatedAt": now,
	})
	for i := 0; i < 15; i++ {
		st := "failed"
		if i > 5 {
			st = "sent"
		}
		_, _ = db.Collection("outboundMessages").InsertOne(context.Background(), bson.M{
			"_id": store.NewID(store.PrefixOutbound), "organizationId": org, "accountId": acc,
			"state": st, "updatedAt": now,
		})
	}
	_, _ = db.Collection("apiKeys").InsertOne(context.Background(), bson.M{
		"_id": keyID, "organizationId": org, "name": "k", "expiresAt": now.Add(48 * time.Hour),
	})
	_, _ = db.Collection("integrationInstances").InsertOne(context.Background(), bson.M{
		"_id": intID, "organizationId": org, "type": "slack", "name": "Slack", "enabled": true,
		"lastActionAt": now, "lastActionOK": false, "lastActionError": "http 500",
	})
	_, _ = db.Collection("migrations").InsertOne(context.Background(), bson.M{"_id": 1, "pending": true})
	_, _ = db.Collection("memberships").InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixMembership), "userId": store.NewID(store.PrefixUser),
		"organizationId": org, "role": "owner",
	})

	paused := now.Add(-2 * time.Hour)
	w := &Watchers{
		Engine: eng, Repo: eng.Repo, DB: db, Log: testLog(),
		BusInfo: func(context.Context) []QueueStat {
			return []QueueStat{{
				Name: "EMAILBRIDGE_SUBMIT", OrgID: org, Pending: 100,
				OldestAge: time.Hour, PausedAt: &paused,
			}}
		},
		ClusterNodes: func(context.Context) []NodeStat {
			return []NodeStat{{ID: "node1", LastSeen: now.Add(-10 * time.Minute)}}
		},
	}
	w.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)

	// sync paths for determinism
	_, _ = eng.RaiseSync(context.Background(), Problem{
		Scope: org, Key: "queue.pending:EMAILBRIDGE_SUBMIT", Category: CatQueues, Severity: SeverityWarning,
		TitleKey: "t", DetailKey: "d",
	})
	_, _ = eng.RaiseSync(context.Background(), Problem{
		Scope: ScopeInstance, Key: "platform.node:node1", Category: CatPlatform, Severity: SeverityCritical,
		TitleKey: "t", DetailKey: "d",
	})

	m := NewMetrics()
	if m.Collector() == nil {
		t.Fatal("collector")
	}
	m.Refresh(context.Background(), eng.Repo)
	counts, err := eng.Repo.OpenGauge(context.Background())
	if err != nil || len(counts) == 0 {
		t.Fatalf("%v %+v", err, counts)
	}

	// Instance owner orgs
	owner := store.NewID(store.PrefixUser)
	_, _ = db.Collection("users").InsertOne(context.Background(), bson.M{"_id": owner, "instanceRole": "owner", "email": "io@t"})
	_, _ = db.Collection("memberships").InsertOne(context.Background(), bson.M{
		"_id": store.NewID(store.PrefixMembership), "userId": owner, "organizationId": org, "role": "owner",
	})
	orgs, err := eng.Repo.InstanceOwnerOrgs(context.Background())
	if err != nil || len(orgs) == 0 {
		t.Fatalf("%v %v", err, orgs)
	}

	// settings helpers
	_, _ = db.Collection("settings").InsertOne(context.Background(), bson.M{
		"_id": "global", "values": bson.M{"notifications.webhooks.failureThreshold": 3, "notifications.watchers.interval": "30s"},
		"version": 1,
	})
	if SettingInt(context.Background(), db, "", "notifications.webhooks.failureThreshold", 5) != 3 {
		t.Fatal("SettingInt")
	}
	if SettingDuration(context.Background(), db, "", "notifications.watchers.interval", time.Minute) != 30*time.Second {
		t.Fatal("SettingDuration")
	}

	// prefs load with muted + quiet
	_, _ = db.Collection("userSettings").InsertOne(context.Background(), bson.M{
		"_id": owner, "settings": bson.M{
			"ui": bson.M{"locale": "en"},
			"notifications": bson.M{
				"mutedCategories": []string{CatPEC},
				"email": bson.M{
					"enabled": true, "digest": "hourly", "minSeverity": "info",
					"categories": bson.M{"webhooks": false},
				},
				"quietHours": bson.M{"start": "22:00", "end": "07:00", "tz": "UTC"},
			},
		}, "version": 1,
	})
	prefs := &Prefs{DB: db, Env: "test"}
	up := prefs.LoadUserPrefs(context.Background(), owner, org)
	if !up.Muted(CatPEC) || up.Categories[CatWebhooks] {
		t.Fatalf("%+v", up)
	}
	if !prefs.EmailSwitchOn(context.Background(), org) {
		t.Fatal("switch default on")
	}

	// flush digests
	mod := &Module{engine: eng, Log: testLog()}
	mod.flushDigests(context.Background())

	// Duty cancel
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Duty(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("duty hang")
	}

	// async Raise/Resolve
	eng.Raise(context.Background(), Problem{
		Scope: org, Key: "async1", Category: CatStorage, Severity: SeverityInfo, TitleKey: "t", DetailKey: "d",
	})
	eng.Resolve(context.Background(), org, "async1")
	time.Sleep(300 * time.Millisecond)

	// OpenKeys + resolveMissing
	w.resolveMissing(context.Background(), CatStorage, map[string]bool{})
}

func TestIntegrationModuleStartMinimal(t *testing.T) {
	db := testDB(t)
	a := app.New(app.Options{Log: testLog(), Roles: app.RoleSet{}})
	app.Provide(a, db)
	mod := &Module{Env: "test", Log: testLog()}
	if err := mod.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if Default() == nil || mod.Engine() == nil {
		t.Fatal("Default engine")
	}
	mctx, cancel := context.WithCancel(context.Background())
	go mod.metricsDuty(mctx)
	go mod.digestDuty(mctx)
	cancel()
	_ = mod.Stop(context.Background())
}

func TestParseHHMMAndRender(t *testing.T) {
	if _, err := parseHHMM("xx"); err == nil {
		t.Fatal()
	}
	if _, err := parseHHMM("25:00"); err == nil {
		t.Fatal()
	}
	_ = errString("x").Error()
	if renderAlertBody(&Notification{DetailKey: "notify.x", Link: "/a"}) == "" {
		t.Fatal()
	}
	if humanizeKey("") == "" {
		t.Fatal()
	}
	if asMap(bson.M{"a": 1}) == nil {
		t.Fatal()
	}
}
