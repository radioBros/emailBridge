//go:build integration

package exports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
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
		URI: uri, Database: fmt.Sprintf("ebtest_exp_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(31).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := crypto.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Migration(8).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := events.Migration(7).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testS3(t *testing.T) *objstore.Store {
	t.Helper()
	ep, ak, sk := containers.S3(t)
	s, err := objstore.Open(objstore.Options{
		Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk,
		Bucket: "emailbridge-exports", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{
		KEK: l, Store: &crypto.MongoKeyStore{DB: db},
		NewID: func() string { return store.NewID(store.PrefixDataKey) },
	}
}

func asOwner(org string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleOwner,
	})
}

func seedMessages(t *testing.T, db *store.DB, org, acc, mbx string, n int) {
	t.Helper()
	ctx := context.Background()
	// The ownership check (G39) counts the account in its organisation: store the account row too.
	_, _ = db.Collection("accounts").UpdateOne(ctx, bson.M{"_id": acc},
		bson.M{"$setOnInsert": bson.M{"organizationId": org}}, options.UpdateOne().SetUpsert(true))
	_, _ = db.Collection(index.Mailboxes).InsertOne(ctx, bson.M{
		"_id": mbx, "organizationId": org, "accountId": acc, "path": "INBOX", "status": "active",
	})
	for i := 1; i <= n; i++ {
		id := store.NewID(store.PrefixMessage)
		_, err := db.Collection(index.Messages).InsertOne(ctx, index.Message{
			ID: id, OrganizationID: org, AccountID: acc, MailboxID: mbx,
			UIDValidity: 1, UID: uint32(i), Flags: []string{`\Seen`},
			EmailID: fmt.Sprintf("e%d", i), InternalDate: time.Date(2026, 10, i, 12, 0, 0, 0, time.UTC),
			Size: int64(100 + i), IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newSvc(t *testing.T, db *store.DB, objs *objstore.Store) (*Service, *jobs.Service) {
	t.Helper()
	js := &jobs.Service{DB: db, NodeID: "test-node", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	svc := &Service{
		Repo: &Repo{DB: db}, Jobs: js, Objects: objs, Cipher: testCipher(t, db),
		Events:  &events.Writer{DB: db, Partitions: 4},
		SignKey: []byte("exports-signed-url-test-key!!!!"), PublicURL: "http://localhost:8080",
		SkipFeatureGate: true, BatchSize: 2,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC) },
	}
	svc.RegisterHandlers()
	return svc, js
}

func TestIntegrationExportResumeNoDupes(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, js := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 5)
	ctx := asOwner(org)

	accp, err := svc.Create(ctx, acc, CreateInput{Include: Include{MetadataOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	expID := accp.Export.ID

	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": expID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, err := svc.Get(ctx, acc, expID)
	if err != nil || done.State != StateSucceeded {
		t.Fatalf("%+v %v", done, err)
	}
	if done.MessageCount != 5 {
		t.Fatalf("messageCount=%d want 5", done.MessageCount)
	}
	if done.Progress == nil || done.Progress.PartCount < 1 {
		t.Fatalf("expected multipart progress: %+v", done.Progress)
	}

	raw := downloadNDJSON(t, svc, ctx, acc, expID)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	uids := map[uint32]int{}
	for _, line := range lines {
		var l Line
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		uids[l.UID]++
	}
	if len(uids) != 5 {
		t.Fatalf("unique uids=%d lines=%d", len(uids), len(lines))
	}
	for u, c := range uids {
		if c != 1 {
			t.Fatalf("uid %d duplicated %d times", u, c)
		}
	}
	_ = mbx
	_ = objs

	su, err := svc.SignedURL(ctx, acc, expID)
	if err != nil || !strings.Contains(su.URL, "/e/") {
		t.Fatalf("%+v %v", su, err)
	}
	tok := strings.TrimPrefix(su.URL, "http://localhost:8080/e/")
	rc2, _, err := svc.DownloadSigned(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc2.Close()

	list, err := svc.List(ctx, acc, ListFilter{IncludeTotal: true, Sort: "-createdAt"})
	if err != nil || len(list.Data) != 1 || list.Total == nil || *list.Total != 1 {
		t.Fatalf("%+v %v", list, err)
	}

	n, err := svc.EraseAccount(ctx, org, acc)
	if err != nil || n != 1 {
		t.Fatalf("erase %d %v", n, err)
	}
	if _, err := svc.Get(ctx, acc, expID); errs.CodeOf(err) != errs.CodeExportNotFound {
		t.Fatalf("after erase: %v", err)
	}
}

func TestIntegrationCancelQueuedAndFeatureGate(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, _ := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := asOwner(org)

	svc.SkipFeatureGate = false
	if _, err := svc.Create(ctx, acc, CreateInput{}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("feature: %v", err)
	}
	svc.SkipFeatureGate = true

	a, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.Cancel(ctx, acc, a.Export.ID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("%+v %v", cancelled, err)
	}
	if _, err := svc.Cancel(ctx, acc, a.Export.ID); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("re-cancel: %v", err)
	}
	if _, _, err := svc.OpenDownload(ctx, acc, a.Export.ID); errs.CodeOf(err) != errs.CodeExportNotReady {
		t.Fatalf("download: %v", err)
	}
}

func TestIntegrationRetentionDuty(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, _ := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := asOwner(org)
	e := &Export{
		ID: store.NewID(store.PrefixExport), OrganizationID: org, AccountID: acc,
		State: StateSucceeded, CreatedAt: svc.now(),
	}
	past := svc.now().Add(-time.Hour)
	e.ExpireAt = &past
	prefix, _ := objstore.ExportPrefix(org, e.ID)
	e.ObjectRef = prefix + "part-000001.ndjson.gz"
	_ = objs.Put(ctx, e.ObjectRef, bytes.NewReader([]byte("x")), 1, objstore.ObjectMeta{Organization: org, Account: acc})
	if err := svc.Repo.insert(ctx, e); err != nil {
		t.Fatal(err)
	}
	svc.sweepRetention(ctx)
	got, err := svc.Repo.getByID(ctx, org, e.ID)
	if err != nil || got.State != StateExpired {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestIntegrationModuleMeta(t *testing.T) {
	m := &Module{SignKey: []byte("k"), PublicURL: "http://x", Log: slog.Default()}
	if m.Name() != "exports" || m.Roles() != nil {
		t.Fatal("module meta")
	}
	_ = m.Stop(context.Background())
}
