//go:build integration

package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator/simtest"
	"github.com/radiobros/emailbridge/test/containers"
)

func init() {
	if os.Getenv("DOCKER_CONFIG") == "" {
		cfg := os.TempDir() + "/eb-empty-docker-config"
		_ = os.MkdirAll(cfg, 0o755)
		_ = os.Setenv("DOCKER_CONFIG", cfg)
	}
}

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_archive_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func testS3(t *testing.T) *objstore.Store {
	t.Helper()
	ep, ak, sk := containers.S3(t)
	s, err := objstore.Open(objstore.Options{
		Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk,
		Bucket: "emailbridge-archive-test", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func rawMsg(subj, mid string) []byte {
	return []byte(fmt.Sprintf("From: a@x\r\nTo: b@x\r\nSubject: %s\r\nMessage-ID: %s\r\n\r\nbody-%s\r\n", subj, mid, subj))
}

type fixedLoader struct{ acc *ebsync.Account }

func (f fixedLoader) Load(_ context.Context, accountID string) (*ebsync.Account, error) {
	if accountID != f.acc.ID {
		return nil, errs.New(errs.CodeNotFound, "missing")
	}
	return f.acc, nil
}

type harness struct {
	db    *store.DB
	store *Store
	pool  *Pool
	svc   *Service
	sim   *simtest.Env
	user  string
	org   string
	accID string
	ctx   context.Context
	acc   *ebsync.Account
}

func setup(t *testing.T) *harness {
	t.Helper()
	db := testDB(t)
	objs := testS3(t)
	sim := simtest.Start(t)
	user := fmt.Sprintf("u%d", time.Now().UnixNano())
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	accID := store.NewID(store.PrefixAccount)
	acc := &ebsync.Account{
		ID: accID, OrganizationID: org, Email: user + "@test",
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	st := &Store{Objects: objs}
	pool := &Pool{Dialer: egress.NewDialer(egress.Policy{}), Idle: 60 * time.Second}
	svc := &Service{
		DB: db, Store: st, Pool: pool, Accounts: fixedLoader{acc},
		Log: slog.New(slog.DiscardHandler),
	}
	cctx := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleOwner,
	})
	return &harness{
		db: db, store: st, pool: pool, svc: svc, sim: sim,
		user: user, org: org, accID: accID, ctx: cctx, acc: acc,
	}
}

func (h *harness) seedIndexed(t *testing.T, uid, uidValidity uint32, archiveRef string, at time.Time) string {
	t.Helper()
	mailboxID := store.NewID(store.PrefixMailbox)
	msgID := store.NewID(store.PrefixMessage)
	_, err := h.db.Collection(index.Mailboxes).InsertOne(context.Background(), bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID,
		"path": "INBOX", "uidValidity": uidValidity, "status": "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID,
		"mailboxId": mailboxID, "uidValidity": uidValidity, "uid": uid,
		"modseq": int64(1), "flags": []string{}, "emailId": "e1",
		"internalDate": at, "size": int64(100), "indexedAt": time.Now().UTC(),
	}
	if archiveRef != "" {
		doc["archiveRef"] = archiveRef
	}
	if _, err := h.db.Collection(index.Messages).InsertOne(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	return msgID
}

func TestIntegrationPutRawAndOpen(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	msgID := store.NewID(store.PrefixMessage)
	body := rawMsg("stored", "<s@x>")
	at := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, at, body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(key, "/2026/04/") || !strings.HasSuffix(key, msgID+".eml") {
		t.Fatalf("key %q", key)
	}
	rc, info, err := h.store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, body) || info.Organization != h.org {
		t.Fatalf("got %q meta %+v", got, info)
	}
	_ = h.store.Delete(ctx, key)
	_ = h.store.Delete(ctx, key) // idempotent
}

func TestIntegrationRawFromMailbox(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	body := rawMsg("live", "<live@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", body, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msgID := h.seedIndexed(t, inj.UID, inj.UIDValidity, "", time.Now().UTC())
	res, err := h.svc.resolveRaw(h.ctx, h.accID, msgID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceMailbox || !bytes.Contains(res.Body, []byte("Subject: live")) {
		t.Fatalf("%+v body=%q", res, res.Body)
	}
}

func TestIntegrationRawFallbackAfterExpunge(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	body := rawMsg("gone", "<gone@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", body, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msgID := store.NewID(store.PrefixMessage)
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := store.NewID(store.PrefixMailbox)
	_, _ = h.db.Collection(index.Mailboxes).InsertOne(ctx, bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID,
		"path": "INBOX", "uidValidity": inj.UIDValidity, "status": "active",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID,
		"mailboxId": mailboxID, "uidValidity": inj.UIDValidity, "uid": inj.UID,
		"modseq": int64(1), "flags": []string{}, "emailId": "e",
		"internalDate": time.Now().UTC(), "size": int64(len(body)),
		"archiveRef": key, "indexedAt": time.Now().UTC(),
	})
	if err := h.sim.Client.SetFlags(ctx, h.user, "INBOX", []uint32{inj.UID}, []string{`\Deleted`}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := h.sim.Client.Expunge(ctx, h.user, "INBOX", []uint32{inj.UID}); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.resolveRaw(h.ctx, h.accID, msgID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceArchive {
		t.Fatalf("source %s", res.Source)
	}
	data, _ := io.ReadAll(res.Reader)
	_ = res.Reader.Close()
	if !bytes.Contains(data, []byte("Subject: gone")) {
		t.Fatalf("%q", data)
	}
}

func TestIntegrationUIDValidityFallback(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	body := rawMsg("uv", "<uv@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", body, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msgID := store.NewID(store.PrefixMessage)
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := store.NewID(store.PrefixMailbox)
	_, _ = h.db.Collection(index.Mailboxes).InsertOne(ctx, bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID,
		"path": "INBOX", "uidValidity": inj.UIDValidity + 99, "status": "active",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID,
		"mailboxId": mailboxID, "uidValidity": inj.UIDValidity + 99, "uid": inj.UID,
		"modseq": int64(1), "flags": []string{}, "emailId": "e",
		"internalDate": time.Now().UTC(), "size": int64(len(body)),
		"archiveRef": key, "indexedAt": time.Now().UTC(),
	})
	res, err := h.svc.resolveRaw(h.ctx, h.accID, msgID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceArchive {
		t.Fatalf("want archive after UIDVALIDITY mismatch, got %s", res.Source)
	}
	_ = res.Reader.Close()
}

func TestIntegrationArchiveOnly404(t *testing.T) {
	h := setup(t)
	msgID := h.seedIndexed(t, 1, 1, "", time.Now().UTC())
	_, err := h.svc.resolveArchive(h.ctx, h.accID, msgID)
	if errs.CodeOf(err) != errs.CodeArchiveNotFound {
		t.Fatalf("%v", err)
	}
	missing := store.NewID(store.PrefixMessage)
	_, err = h.svc.resolveArchive(h.ctx, h.accID, missing)
	if errs.CodeOf(err) != errs.CodeArchiveNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationTenantIsolation(t *testing.T) {
	h := setup(t)
	body := rawMsg("iso", "<iso@x>")
	msgID := store.NewID(store.PrefixMessage)
	key, err := h.store.PutRaw(context.Background(), h.org, h.accID, msgID, time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.seedIndexed // silence
	mailboxID := store.NewID(store.PrefixMailbox)
	_, _ = h.db.Collection(index.Mailboxes).InsertOne(context.Background(), bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID, "path": "INBOX", "status": "active",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(context.Background(), bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID, "mailboxId": mailboxID,
		"uidValidity": uint32(1), "uid": uint32(1), "archiveRef": key,
		"internalDate": time.Now().UTC(), "size": int64(1), "indexedAt": time.Now().UTC(),
		"emailId": "e", "flags": []string{}, "modseq": int64(1),
	})
	otherOrg := store.NewID(store.PrefixOrganization)
	otherCtx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: otherOrg, Role: tenancy.RoleOwner,
	})
	_, err = h.svc.resolveRaw(otherCtx, h.accID, msgID)
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("want not found across tenants, got %v", err)
	}
}

func TestIntegrationRetentionSweep(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-48 * time.Hour)
	msgID := store.NewID(store.PrefixMessage)
	body := rawMsg("old", "<old@x>")
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, old, body)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.db.Collection("accounts").InsertOne(ctx, bson.M{
		"_id": h.accID, "organizationId": h.org, "archiveRetention": "24h",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID,
		"mailboxId": "mb", "uidValidity": uint32(1), "uid": uint32(1),
		"archiveRef": key, "internalDate": old, "size": int64(1),
		"indexedAt": old, "emailId": "e", "flags": []string{}, "modseq": int64(1),
	})
	keepID := store.NewID(store.PrefixMessage)
	keepKey, _ := h.store.PutRaw(ctx, h.org, h.accID, keepID, time.Now().UTC(), body)
	_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
		"_id": keepID, "organizationId": h.org, "accountId": h.accID,
		"mailboxId": "mb", "uidValidity": uint32(1), "uid": uint32(2),
		"archiveRef": keepKey, "internalDate": time.Now().UTC(), "size": int64(1),
		"indexedAt": time.Now().UTC(), "emailId": "e2", "flags": []string{}, "modseq": int64(1),
	})
	// account with retention 0 is skipped
	_, _ = h.db.Collection("accounts").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAccount), "organizationId": h.org, "archiveRetention": "0",
	})

	h.svc.SweepRetention(ctx)

	_, _, err = h.store.Open(ctx, key)
	if err == nil {
		t.Fatal("old archive should be gone")
	}
	var m bson.M
	_ = h.db.Collection(index.Messages).FindOne(ctx, bson.M{"_id": msgID}).Decode(&m)
	if _, ok := m["archiveRef"]; ok {
		t.Fatalf("archiveRef not cleared: %+v", m)
	}
	rc, _, err := h.store.Open(ctx, keepKey)
	if err != nil {
		t.Fatal("recent archive must remain")
	}
	_ = rc.Close()
}

func TestIntegrationPoolReuseAndIdleClose(t *testing.T) {
	h := setup(t)
	h.pool.Idle = 80 * time.Millisecond
	ctx := context.Background()
	body := rawMsg("pool", "<pool@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", body, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = h.seedIndexed(t, inj.UID, inj.UIDValidity, "", time.Now().UTC())

	l1, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l1.Release()
	l2, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()

	held, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Acquire(ctx, h.acc); !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("want busy, got %v", err)
	}
	held.Release()

	time.Sleep(100 * time.Millisecond)
	h.pool.Sweep()
	l3, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l3.Discard()
	h.pool.Close()
}

func TestIntegrationAPIRawAndArchive(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	body := rawMsg("api", "<api@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", body, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msgID := h.seedIndexed(t, inj.UID, inj.UIDValidity, "", time.Now().UTC())

	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Register(srv)

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(h.ctx)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}

	rr := do(fmt.Sprintf("/v1/accounts/%s/messages/%s/raw", h.accID, msgID))
	if rr.Code != http.StatusOK {
		t.Fatalf("raw status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "message/rfc822") {
		t.Fatalf("ct %q", rr.Header().Get("Content-Type"))
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), msgID+".eml") {
		t.Fatalf("cd %q", rr.Header().Get("Content-Disposition"))
	}
	if rr.Header().Get("EB-Source") != SourceMailbox {
		t.Fatalf("EB-Source %q", rr.Header().Get("EB-Source"))
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Subject: api")) {
		t.Fatalf("%s", rr.Body.String())
	}

	// archive-only without archiveRef → 404 archive_not_found
	rr = do(fmt.Sprintf("/v1/accounts/%s/archive/%s", h.accID, msgID))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("archive status %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "archive_not_found") {
		t.Fatalf("%s", rr.Body.String())
	}

	// account-bound key on another account
	bound := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_x", OrganizationID: h.org,
		Scopes: []string{"accounts:read", "read"}, AccountID: store.NewID(store.PrefixAccount),
	})
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/accounts/%s/messages/%s/raw", h.accID, msgID), nil).WithContext(bound)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bound key status %d %s", rr.Code, rr.Body.String())
	}
}

func TestIntegrationModuleWiring(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	quiet := slog.New(slog.DiscardHandler)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, objs)
	app.Provide(a, egress.NewDialer(egress.Policy{}))
	app.Provide(a, &scheduler.Scheduler{})
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)

	m := &Module{
		Accounts: fixedLoader{&ebsync.Account{ID: "acc_x"}},
		Log:      quiet, Idle: time.Second,
	}
	ctx := context.Background()
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Archiver() == nil || m.Service() == nil {
		t.Fatal("not started")
	}
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	// RetentionDuty exits when ctx cancelled
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.Service().RetentionEvery = time.Hour
	go func() { defer close(done); m.Service().RetentionDuty(rctx) }()
	cancel()
	<-done
	_ = m.Stop(ctx)
}
