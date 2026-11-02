//go:build integration

package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/test/containers"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_accounts_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	ctx := context.Background()
	ks := &crypto.MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(ctx, crypto.DataKeysCollection, ks.Indexes()...); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, crypto.KeySize)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
}

func testSvc(t *testing.T) (*Service, context.Context, string) {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()
	if err := EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	_ = audit.Migration(1).Up(ctx, db)
	cipher := testCipher(t, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := &events.Writer{DB: db}
	js := &jobs.Service{DB: db, NodeID: "n1", Log: quiet}
	aud := &audit.Writer{DB: db, Log: quiet, Retention: 24 * time.Hour}
	svc := &Service{
		DB: db, Cipher: cipher, Events: ev, Jobs: js, Audit: aud, Log: quiet,
		Egress: egress.NewDialer(egress.Policy{}),
	}
	svc.RegisterHandlers()
	org := store.NewID(store.PrefixOrganization)
	user := store.NewID(store.PrefixUser)
	cctx := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: user, OrganizationID: org, Role: tenancy.RoleOwner,
	})
	return svc, cctx, org
}

func sampleCreate(ext string) CreateInput {
	return CreateInput{
		Name: "Test", Email: "test@example.com", ExternalID: ext, SkipVerify: true,
		IMAP: IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "secret-pass"}},
		SMTP: SMTPConfigInput{Host: "127.0.0.1", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "secret-pass"}, Policy: PolicyStandard},
	}
}

func waitJob(t *testing.T, svc *Service, ctx context.Context, id, state string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		j, err := svc.Jobs.Get(ctx, id)
		if err == nil && j.State == state {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s not %s: %+v %v", id, state, j, err)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func TestIntegrationCRUDExternalIDEventsErasure(t *testing.T) {
	svc, ctx, org := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("ext-1"))
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateNew || a.IMAP.Auth.Pass.IsZero() {
		t.Fatalf("%+v", a)
	}
	b, _ := json.Marshal(a.View())
	if strings.Contains(string(b), "secret-pass") || strings.Contains(string(b), `"pass"`) {
		t.Fatalf("password leaked: %s", b)
	}

	if _, err := svc.Create(ctx, sampleCreate("ext-1")); errs.CodeOf(err) != errs.CodeExternalIDTaken {
		t.Fatalf("want external_id_taken, got %v", err)
	}

	n, err := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"accountId": a.ID, "type": events.AccountAdded})
	if err != nil || n != 1 {
		t.Fatalf("account.added count=%d err=%v", n, err)
	}

	before, _ := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"organizationId": org})
	_ = svc.DB.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := svc.Events.Emit(txCtx, events.Draft{
			Type: events.AccountUpdated, OrganizationID: org, AccountID: a.ID, Subject: a.ID,
			Data: &eventtypes.AccountUpdated{
				AccountRef: eventtypes.AccountRef{AccountID: a.ID}, Changed: []string{"name"},
			},
		}); err != nil {
			return err
		}
		return fmt.Errorf("rollback")
	})
	after, _ := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"organizationId": org})
	if after != before {
		t.Fatalf("aborted tx leaked events: before=%d after=%d", before, after)
	}

	got, err := svc.Get(ctx, a.ID)
	if err != nil || got.Email != a.Email {
		t.Fatalf("%+v %v", got, err)
	}

	name := "Renamed"
	patched, err := svc.Patch(ctx, a.ID, PatchInput{Name: &name}, formatETag(a.Version))
	if err != nil || patched.Name != "Renamed" || patched.Version != a.Version+1 {
		t.Fatalf("%+v %v", patched, err)
	}

	list, err := svc.List(ctx, ListFilter{Q: "Ren", Sort: "name"})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("%+v %v", list, err)
	}

	_, _ = svc.DB.Collection("mailboxes").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixMailbox), "accountId": a.ID, "organizationId": org})
	_, _ = svc.DB.Collection("messages").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixMessage), "accountId": a.ID, "organizationId": org})

	job, err := svc.Delete(ctx, a.ID, formatETag(patched.Version))
	if err != nil || job == nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Jobs.Run(rctx, 1) }()
	defer func() { cancel(); <-done }()
	waitJob(t, svc, ctx, job.ID, jobs.Succeeded)

	if err := svc.coll().FindOne(ctx, bson.M{"_id": a.ID}).Err(); err == nil {
		t.Fatal("account still present")
	}
	if n, _ := svc.DB.Collection("mailboxes").CountDocuments(ctx, bson.M{"accountId": a.ID}); n != 0 {
		t.Fatal("mailboxes remain")
	}
	if n, _ := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"type": events.AccountDeleted, "accountId": a.ID}); n != 1 {
		t.Fatal("account.deleted missing")
	}

	j2, err := svc.Jobs.Create(ctx, "erasure", map[string]string{"accountId": a.ID, "organizationId": org})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, ctx, j2.ID, jobs.Succeeded)
}

func TestIntegrationTenantIsolationAndRBAC(t *testing.T) {
	svc, ctxA, _ := testSvc(t)
	a, err := svc.Create(ctxA, sampleCreate("a1"))
	if err != nil {
		t.Fatal(err)
	}
	orgB := store.NewID(store.PrefixOrganization)
	ctxB := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: orgB, Role: tenancy.RoleOwner,
	})
	if _, err := svc.Get(ctxB, a.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("tenant leak: %v", err)
	}

	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: a.OrganizationID, Role: tenancy.RoleViewer,
	})
	if _, err := svc.Get(viewer, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(viewer, sampleCreate("v")); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer create: %v", err)
	}
	if _, err := svc.Sync(viewer, a.ID, SyncInput{Mode: "fast"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer sync: %v", err)
	}

	key := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: a.OrganizationID, Scopes: []string{"accounts:write"}, AccountID: a.ID,
	})
	if _, err := svc.Get(key, a.ID); err != nil {
		t.Fatal(err)
	}
	other := store.NewID(store.PrefixAccount)
	if _, err := svc.Get(key, other); errs.CodeOf(err) != errs.CodeForbidden && errs.CodeOf(err) != errs.CodeNotFound {
		// bound key must refuse other account before/at load
		if errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatalf("bound key other: %v", err)
		}
	}

	// disable + resume
	dis := true
	disabled, err := svc.Patch(ctxA, a.ID, PatchInput{Disabled: &dis}, "")
	if err != nil || disabled.State != StateDisabled {
		t.Fatalf("%+v %v", disabled, err)
	}
	resumed, err := svc.Resume(ctxA, a.ID)
	if err != nil || resumed.State != StateConnecting {
		t.Fatalf("%+v %v", resumed, err)
	}
	if _, err := svc.Resume(ctxA, a.ID); errs.CodeOf(err) != errs.CodeAccountNotDisabled {
		t.Fatalf("resume twice: %v", err)
	}

	job, err := svc.Sync(ctxA, a.ID, SyncInput{Mode: "full"})
	if err != nil || job == nil {
		t.Fatal(err)
	}
	job2, err := svc.Reconnect(ctxA, a.ID)
	if err != nil || job2 == nil {
		t.Fatal(err)
	}
}

func TestIntegrationVerifySMTPAndSSRF(t *testing.T) {
	svc, ctx, _ := testSvc(t)

	// Accepting SMTP without importing go-smtp: open a raw listener that speaks a minimal SMTP dialogue.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go serveTinySMTP(t, ln)

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := svc.Verify(ctx, VerifyRequest{
		SMTP: &SMTPConfigInput{Host: "127.0.0.1", Port: port, TLS: TLSNone, Auth: MailAuthInput{User: "u", Pass: "p"}},
	})
	if err != nil || ok.SMTP == nil || !ok.SMTP.OK {
		t.Fatalf("%+v %v", ok, err)
	}

	bad, err := svc.Verify(ctx, VerifyRequest{
		SMTP: &SMTPConfigInput{Host: "127.0.0.1", Port: port, TLS: TLSNone, Auth: MailAuthInput{User: "u", Pass: "wrong"}},
	})
	if err != nil || bad.SMTP == nil || bad.SMTP.OK {
		t.Fatalf("auth should fail: %+v %v", bad, err)
	}

	ssrf, err := svc.Verify(ctx, VerifyRequest{
		IMAP: &IMAPConfigInput{Host: "169.254.169.254", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u", Pass: "p"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ssrf.IMAP == nil || ssrf.IMAP.OK || !strings.Contains(ssrf.IMAP.Detail, "blocked") {
		t.Fatalf("SSRF not refused: %+v", ssrf.IMAP)
	}

	fail, err := svc.Verify(ctx, VerifyRequest{
		SMTP: &SMTPConfigInput{Host: "127.0.0.1", Port: 1, TLS: TLSNone, Auth: MailAuthInput{User: "u", Pass: "p"}},
	})
	if err != nil || fail.SMTP == nil || fail.SMTP.OK {
		t.Fatalf("connect should fail: %+v %v", fail, err)
	}
}

func TestIntegrationVerifyIMAPDovecot(t *testing.T) {
	addr := os.Getenv("EB_TEST_IMAP_ADDR")
	pass := os.Getenv("EB_TEST_IMAP_PASSWORD")
	if addr == "" {
		d := containers.DovecotServer(t)
		addr, pass = d.IMAPS, d.Password
	}
	if pass == "" {
		pass = "spikepass"
	}
	svc, ctx, _ := testSvc(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Verify(ctx, VerifyRequest{
		IMAP:             &IMAPConfigInput{Host: host, Port: port, TLS: TLSImplicit, Auth: MailAuthInput{User: "verify-user", Pass: pass}},
		IgnoreCertErrors: true,
	})
	if err != nil || res.IMAP == nil || !res.IMAP.OK {
		t.Fatalf("dovecot verify: %+v %v", res, err)
	}
	bad, err := svc.Verify(ctx, VerifyRequest{
		IMAP:             &IMAPConfigInput{Host: host, Port: port, TLS: TLSImplicit, Auth: MailAuthInput{User: "verify-user", Pass: "wrong"}},
		IgnoreCertErrors: true,
	})
	if err != nil || bad.IMAP == nil || bad.IMAP.OK {
		t.Fatalf("wrong password accepted: %+v %v", bad, err)
	}
}

func TestIntegrationTransitionFencing(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("fence"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a.Owner = &Owner{NodeID: "n1", Epoch: 3, Since: now}
	a.State = StateConnecting
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)

	if _, err := svc.Transition(ctx, a.ID, 2, StateConnected, nil); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("bad epoch: %v", err)
	}
	out, err := svc.Transition(ctx, a.ID, 3, StateConnected, &StateReason{Kind: "ok", Code: "indexed", Message: "ok"})
	if err != nil || out.State != StateConnected {
		t.Fatalf("%+v %v", out, err)
	}
	// Transition must not clobber engine fields written without a version bump.
	syncAt := now.Add(-time.Minute).UTC().Truncate(time.Millisecond)
	_, _ = svc.coll().UpdateOne(ctx, bson.M{"_id": a.ID}, bson.M{"$set": bson.M{
		"owner": Owner{NodeID: "n1", Epoch: 3, Since: now}, "lastSyncAt": syncAt,
		"capabilities": bson.M{"IDLE": true},
	}})
	out2, err := svc.Transition(ctx, a.ID, 3, StateConnectError, nil)
	if err != nil || out2.State != StateConnectError {
		t.Fatalf("%+v %v", out2, err)
	}
	got, _ := svc.load(ctx, a.ID)
	if got.Owner == nil || got.Owner.Epoch != 3 || got.LastSyncAt == nil || !got.LastSyncAt.Equal(syncAt) {
		t.Fatalf("transition clobbered engine fields: owner=%+v lastSyncAt=%v", got.Owner, got.LastSyncAt)
	}
	if got.Capabilities["IDLE"] != true {
		t.Fatalf("capabilities lost: %+v", got.Capabilities)
	}
	n, _ := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"accountId": a.ID, "type": events.AccountStateChanged})
	if n < 1 {
		t.Fatal("state_changed missing")
	}
}

// plantEngineSide writes owner/lastSyncAt/recentErrors like the sync engine (no version bump).
func plantEngineSide(t *testing.T, svc *Service, ctx context.Context, id string) (epoch uint64, syncAt time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	epoch = 99
	syncAt = now
	_, err := svc.coll().UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{
			"owner":            Owner{NodeID: "engine-n1", Epoch: epoch, Since: now},
			"lastSyncAt":       syncAt,
			"lastErrorAt":      syncAt,
			"capabilities":     bson.M{"IDLE": true, "UIDPLUS": true},
			"authFailingSince": syncAt,
		},
		"$push": bson.M{"recentErrors": bson.M{
			"$each":  []bson.M{{"at": syncAt, "kind": "transient", "code": "mail_unavailable", "protocol": "imap", "response": "temp fail"}},
			"$slice": -20,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return epoch, syncAt
}

func assertEngineSideSurvived(t *testing.T, a *Account, epoch uint64, syncAt time.Time) {
	t.Helper()
	if a.Owner == nil || a.Owner.Epoch != epoch || a.Owner.NodeID != "engine-n1" {
		t.Fatalf("owner clobbered: %+v want epoch=%d", a.Owner, epoch)
	}
	if a.LastSyncAt == nil || !a.LastSyncAt.Equal(syncAt) {
		t.Fatalf("lastSyncAt clobbered: %v want %v", a.LastSyncAt, syncAt)
	}
	if a.LastErrorAt == nil || !a.LastErrorAt.Equal(syncAt) {
		t.Fatalf("lastErrorAt clobbered: %v", a.LastErrorAt)
	}
	if len(a.RecentErrors) == 0 || a.RecentErrors[0].Code != "mail_unavailable" {
		t.Fatalf("recentErrors clobbered: %+v", a.RecentErrors)
	}
	if a.Capabilities["IDLE"] != true {
		t.Fatalf("capabilities clobbered: %+v", a.Capabilities)
	}
}

func TestIntegrationUpdateAPIVersionAndPush(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("upd-api"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.updateAPI(ctx, a.ID, a.Version+50, bson.M{"name": "nope"}, nil, nil); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("want version_mismatch, got %v", err)
	}
	pc := PendingCommand{Cmd: CmdResync, At: time.Now().UTC(), By: "t", Arg: map[string]any{"mode": "full"}}
	if err := svc.updateAPI(ctx, a.ID, a.Version, bson.M{"name": "Pushed"}, nil, &pc); err != nil {
		t.Fatal(err)
	}
	got, err := svc.load(ctx, a.ID)
	if err != nil || got.Name != "Pushed" || got.Version != a.Version+1 {
		t.Fatalf("%+v %v", got, err)
	}
	if len(got.PendingCommands) == 0 || got.PendingCommands[0].Cmd != CmdResync {
		t.Fatalf("push: %+v", got.PendingCommands)
	}
}

func TestIntegrationAPIWritesPreserveEngineFields(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("preserve-engine"))
	if err != nil {
		t.Fatal(err)
	}

	epoch, syncAt := plantEngineSide(t, svc, ctx, a.ID)
	name := "Patched-Name"
	patched, err := svc.Patch(ctx, a.ID, PatchInput{Name: &name}, formatETag(a.Version))
	if err != nil || patched.Name != name {
		t.Fatalf("patch: %+v %v", patched, err)
	}
	got, err := svc.load(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEngineSideSurvived(t, got, epoch, syncAt)
	if got.AuthFailingSince == nil || !got.AuthFailingSince.Equal(syncAt) {
		t.Fatalf("authFailingSince clobbered by patch: %v", got.AuthFailingSince)
	}

	epoch, syncAt = plantEngineSide(t, svc, ctx, a.ID)
	if _, err := svc.Sync(ctx, a.ID, SyncInput{}); err != nil {
		t.Fatalf("sync empty mode: %v", err)
	}
	got, _ = svc.load(ctx, a.ID)
	assertEngineSideSurvived(t, got, epoch, syncAt)
	if len(got.PendingCommands) == 0 || got.PendingCommands[len(got.PendingCommands)-1].Cmd != CmdResync {
		t.Fatalf("resync not pushed: %+v", got.PendingCommands)
	}
	argMode, _ := got.PendingCommands[len(got.PendingCommands)-1].Arg["mode"].(string)
	if argMode != "fast" {
		t.Fatalf("default sync mode want fast, got %q", argMode)
	}

	epoch, syncAt = plantEngineSide(t, svc, ctx, a.ID)
	ver := got.Version
	if _, err := svc.Reconnect(ctx, a.ID); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	got, _ = svc.load(ctx, a.ID)
	assertEngineSideSurvived(t, got, epoch, syncAt)
	if got.Version != ver+1 {
		t.Fatalf("version: %d want %d", got.Version, ver+1)
	}

	dis := true
	if _, err := svc.Patch(ctx, a.ID, PatchInput{Disabled: &dis}, ""); err != nil {
		t.Fatal(err)
	}
	epoch, syncAt = plantEngineSide(t, svc, ctx, a.ID)
	resumed, err := svc.Resume(ctx, a.ID)
	if err != nil || resumed.State != StateConnecting {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	got, _ = svc.load(ctx, a.ID)
	assertEngineSideSurvived(t, got, epoch, syncAt)
	// Resume intentionally clears the auth-failure clock so disable does not re-fire.
	if got.AuthFailingSince != nil {
		t.Fatalf("resume should unset authFailingSince, got %v", got.AuthFailingSince)
	}
}

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cipher := testCipher(t, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, cipher)
	app.Provide(a, &events.Writer{DB: db})
	js := &jobs.Service{DB: db, NodeID: "n", Log: quiet}
	app.Provide(a, js)
	app.Provide(a, egress.NewDialer(egress.Policy{}))
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	mod := &Module{Log: quiet}
	if err := mod.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if mod.Service() == nil || mod.Name() != "accounts" {
		t.Fatal("module")
	}
	_ = mod.Stop(ctx)
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// serveTinySMTP is a minimal SMTP server for verify tests (no go-smtp import — depguard).
func serveTinySMTP(t *testing.T, ln net.Listener) {
	t.Helper()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			w := bufio.NewWriter(conn)
			r := bufio.NewReader(conn)
			write := func(s string) {
				_, _ = w.WriteString(s + "\r\n")
				_ = w.Flush()
			}
			write("220 tiny.smtp ESMTP")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimSpace(line)
				upper := strings.ToUpper(line)
				switch {
				case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
					write("250-tiny.smtp")
					write("250-AUTH PLAIN")
					write("250 OK")
				case strings.HasPrefix(upper, "AUTH PLAIN"):
					parts := strings.Fields(line)
					ok := false
					if len(parts) >= 3 {
						raw, _ := base64.StdEncoding.DecodeString(parts[2])
						// PLAIN: \0user\0pass
						if string(raw) == "\x00u\x00p" {
							ok = true
						}
					}
					if ok {
						write("235 OK")
					} else {
						write("535 5.7.8 Authentication failed")
					}
				case strings.HasPrefix(upper, "QUIT"):
					write("221 bye")
					return
				default:
					write("250 OK")
				}
			}
		}(c)
	}
}
