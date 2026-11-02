//go:build integration

package sandbox

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/test/containers"
)

func TestIntegrationModuleAndAPIHandlers(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	capID := list.Data[0].ID

	mod := &Module{
		service: h.svc, gate: h.gate, Env: "development", Retention: time.Hour,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mod.Name() != "sandbox" || mod.Roles() != nil || mod.Gate() != h.gate || mod.Service() != h.svc {
		t.Fatal("module accessors")
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	server, err := httpapi.New(httpapi.Options{PublicURL: "http://localhost", MaxBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	mod.registerAPI(server)

	out, err := mod.list(h.ctx, &listInput{Limit: 10, Sort: "-capturedAt", AccountID: h.acc.ID, Text: "Sandbox"})
	if err != nil || len(out.Body.Data) == 0 {
		t.Fatalf("list handler: %+v (%v)", out, err)
	}
	got, err := mod.get(h.ctx, &captureIDInput{CaptureID: capID})
	if err != nil || got.Body.ID != capID {
		t.Fatalf("get handler: %+v (%v)", got, err)
	}
	stream, err := mod.raw(h.ctx, &captureIDInput{CaptureID: capID})
	if err != nil || stream == nil {
		t.Fatalf("raw handler: %v", err)
	}
	if _, err := mod.deleteMessage(h.ctx, &deleteInput{CaptureID: capID}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationModuleStartFull(t *testing.T) {
	h := newHarness(t, "development", false)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ep, ak, sk := containers.S3(t)
	s3, err := objstore.Open(objstore.Options{
		Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk,
		Bucket: "eb-sandbox-mod", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.New(httpapi.Options{PublicURL: "http://localhost", MaxBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	roles, err := app.ParseRoles("api")
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: roles, NodeID: "n1", Log: quiet})
	app.Provide(a, h.db)
	app.Provide(a, s3)
	app.Provide(a, &events.Writer{DB: h.db, Partitions: 4})
	app.Provide(a, server)
	sched := &scheduler.Scheduler{}
	app.Provide(a, sched)
	settingsSvc := settings.NewService(h.db, nil, nil, nil)
	app.Provide(a, settingsSvc)
	mod := &Module{Env: "development", Retention: time.Hour, RetentionEvery: time.Minute, Log: quiet}
	if err := mod.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if mod.Gate() == nil || mod.Service() == nil || mod.Gate().Settings == nil {
		t.Fatal("gate/service/settings not wired")
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRetentionDutyAndSweep(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	capID := list.Data[0].ID
	past := time.Now().UTC().Add(-time.Minute)
	_, _ = h.db.Collection(Collection).UpdateOne(h.ctx, bson.M{"_id": capID}, bson.M{"$set": bson.M{"expireAt": past}})

	ctx, cancel := context.WithCancel(context.Background())
	h.svc.RetentionEvery = 20 * time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); h.svc.RetentionDuty(ctx) }()
	deadline := time.After(2 * time.Second)
	for {
		n, _ := h.db.Collection(Collection).CountDocuments(h.ctx, bson.M{"_id": capID})
		if n == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retention duty did not delete")
		case <-time.After(30 * time.Millisecond):
		}
	}
	cancel()
	<-done
	h.svc.SweepRetention(context.Background())
}

func TestIntegrationSettingsRewriteToAndUnknownPolicy(t *testing.T) {
	h := newHarness(t, "development", false)
	boot := settings.NewService(h.db, nil, nil, nil)
	boot.EnvName = "development"
	h.gate.Settings = boot

	_, err := h.db.Collection(settings.SettingsCollection).ReplaceOne(h.ctx,
		bson.M{"_id": settings.OrgDocID(h.org)},
		bson.M{
			"_id": settings.OrgDocID(h.org), "version": int64(1),
			"switches": bson.M{
				switchEnabled: bson.M{"value": true},
				switchPolicy:  bson.M{"value": PolicyRewrite},
			},
			"values": bson.M{settingRewrite: rewrite},
		}, options.Replace().SetUpsert(true))
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.gate.resolve(h.ctx, h.org, h.acc.ID)
	if err != nil || !d.Enabled || d.Policy != PolicyRewrite || d.RewriteTo != rewrite {
		t.Fatalf("resolve via settings: %+v (%v)", d, err)
	}

	_, _ = h.db.Collection(settings.SettingsCollection).ReplaceOne(h.ctx,
		bson.M{"_id": settings.OrgDocID(h.org)},
		bson.M{
			"_id": settings.OrgDocID(h.org), "version": int64(1),
			"switches": bson.M{
				switchEnabled: bson.M{"value": true},
				switchPolicy:  bson.M{"value": "nonsense"},
			},
			"values": bson.M{},
		}, options.Replace().SetUpsert(true))
	id := h.submitMsg()
	h.work()
	m := h.getOutbound(id)
	if m.State == submit.StateSent || m.State == submit.StateCaptured {
		t.Fatalf("unknown policy should not succeed cleanly: %s", m.State)
	}
	if len(m.Attempts) == 0 {
		t.Fatal("expected attempt")
	}
}

func TestIntegrationListCursorAndDefaults(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	for range 3 {
		h.submitMsg()
		h.work()
	}
	page, err := h.svc.List(h.ctx, ListFilter{Limit: 1, Sort: "-capturedAt"})
	if err != nil || page.NextCursor == nil {
		t.Fatalf("%+v (%v)", page, err)
	}
	next, err := h.svc.List(h.ctx, ListFilter{Limit: 1, Sort: "-capturedAt", Cursor: *page.NextCursor})
	if err != nil || len(next.Data) != 1 {
		t.Fatalf("%+v (%v)", next, err)
	}
	if next.Data[0].ID == page.Data[0].ID {
		t.Fatal("cursor did not advance")
	}

	g := &Gate{DB: h.db, Objects: h.objects, Events: h.gate.Events}
	if g.now().IsZero() || g.log() == nil || g.registry() == nil || g.env() != "production" || g.retention() != 14*24*time.Hour {
		t.Fatal("gate defaults")
	}
	_ = h.svc.now()
	_ = h.svc.log()
}

func TestIntegrationAPIValidationPaths(t *testing.T) {
	h := newHarness(t, "development", false)
	if _, err := h.svc.Get(h.ctx, "not-a-cap-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if err := h.svc.Delete(h.ctx, "not-a-cap-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := h.svc.List(h.ctx, ListFilter{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	capID := store.NewID(store.PrefixCapture)
	_, err := h.db.Collection(Collection).InsertOne(h.ctx, Captured{
		ID: capID, OrganizationID: h.org, AccountID: h.acc.ID, OutboundID: store.NewID(store.PrefixOutbound),
		CapturedAt: time.Now().UTC(), ExpireAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.Raw(h.ctx, capID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("raw without ref: %v", err)
	}
}

func TestIntegrationGlobalSwitchDocs(t *testing.T) {
	h := newHarness(t, "production", false)
	_, err := h.db.Collection(settings.SettingsCollection).ReplaceOne(h.ctx,
		bson.M{"_id": settings.DocIDGlobal},
		bson.M{
			"_id": settings.DocIDGlobal, "version": int64(1),
			"switches": bson.M{
				switchEnabled: bson.M{"value": true, "locked": true},
				switchPolicy:  bson.M{"value": PolicyDrop},
			},
			"values": bson.M{},
		}, options.Replace().SetUpsert(true))
	if err != nil {
		t.Fatal(err)
	}
	h.clearSMTP()
	id := h.submitMsg()
	h.work()
	if h.getOutbound(id).State != submit.StateCaptured {
		t.Fatal("locked global enable + drop")
	}
	msgs, _ := h.sim.Client.ListSMTPMessages(h.ctx)
	if len(msgs) != 0 {
		t.Fatal("drop must not send")
	}
}
