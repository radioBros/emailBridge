//go:build integration

package attachments

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
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
		URI: uri, Database: fmt.Sprintf("ebtest_att_%d", time.Now().UnixNano()),
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
		Bucket: "emailbridge-att-test", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func ids() (org, acc, msg string) {
	return "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
}

func TestIntegrationIngestS3SignedURLAndErase(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	org, acc, msg := ids()
	ctx := store.WithOrganization(context.Background(), org)
	s := &Service{
		DB: db, Objects: objs,
		SignKey:   []byte("integration-sign-key-32-bytes!!"),
		PublicURL: "https://eb.example", SignedTTL: 5 * time.Minute,
		Raw: fakeRaw{body: sampleMIME()},
		Now: func() time.Time { return time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC) },
		Log: slog.Default(),
	}
	s.init()
	if err := s.store.ensure(ctx); err != nil {
		t.Fatal(err)
	}

	res, err := s.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), StoreS3: true,
	})
	if err != nil || len(res) != 1 || res[0].URL == "" || res[0].Scan != ScanSkipped {
		t.Fatalf("%+v %v", res, err)
	}
	d, rc, err := s.Open(ctx, acc, msg, res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "pdf" || d.ObjectKey == "" || d.ETag == "" {
		t.Fatalf("%q %+v", body, d)
	}

	su, err := s.SignedURL(ctx, acc, msg, res[0].ID)
	if err != nil || !strings.HasPrefix(su.URL, "https://eb.example/a/") {
		t.Fatalf("%+v %v", su, err)
	}
	tok := strings.TrimPrefix(su.URL, "https://eb.example/a/")
	d2, rc, err := s.OpenSigned(ctx, tok)
	if err != nil || d2.ID != res[0].ID {
		t.Fatalf("%v %+v", err, d2)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "pdf" {
		t.Fatalf("%q", got)
	}

	// Org binding: token for another org fails
	badTok, _ := Sign(s.SignKey, "org_01zzzzzzzzzzzzzzzzzzzzzzzz", res[0].ID, s.Now().Add(time.Hour))
	if _, _, err := s.OpenSigned(ctx, badTok); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatalf("cross-org: %v", err)
	}

	n, err := s.EraseMessage(ctx, acc, msg)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := objs.Stat(ctx, d.ObjectKey); err == nil {
		t.Fatal("s3 object survived erase")
	}
	if _, _, err := s.Open(ctx, acc, msg, res[0].ID); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
}

func TestIntegrationModuleStartAndPublicRoute(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	org, acc, msg := ids()
	ctx := store.WithOrganization(context.Background(), org)

	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "att-n1", Log: slog.Default()})
	app.Provide(a, db)
	app.Provide(a, objs)
	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)

	m := &Module{
		SignKey:   []byte("module-sign-key-32-bytes-long!!!"),
		PublicURL: "https://eb.example", SignedTTL: time.Minute,
		Raw: fakeRaw{body: sampleMIME()}, Log: slog.Default(),
	}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()
	svc := m.Service()
	if svc == nil {
		t.Fatal("nil service")
	}
	res, err := svc.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), StoreS3: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := Sign(m.SignKey, org, res[0].ID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/a/"+tok, nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pdf") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if et := rec.Header().Get("ETag"); et == "" {
		t.Fatal("missing ETag")
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "a.pdf") {
		t.Fatalf("disposition %q", cd)
	}
}

func TestIntegrationClamAVEICAR(t *testing.T) {
	addr := containers.ClamAV(t)
	s := &Scanner{Addr: addr, Timeout: 2 * time.Minute, MaxSize: 1 << 20}
	// Standard EICAR test string (not a real virus).
	eicar := []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)
	deadline := time.Now().Add(3 * time.Minute)
	var r ScanResult
	for {
		r = s.Scan(context.Background(), bytes.NewReader(eicar), int64(len(eicar)))
		if r.Status == ScanInfected || time.Now().After(deadline) {
			break
		}
		// clamd may still be loading signatures right after listen.
		if r.Status == ScanError {
			time.Sleep(2 * time.Second)
			continue
		}
		break
	}
	if r.Status != ScanInfected {
		t.Fatalf("eicar: %+v", r)
	}
	clean := []byte("hello-clean-payload")
	r2 := s.Scan(context.Background(), bytes.NewReader(clean), int64(len(clean)))
	if r2.Status != ScanClean {
		t.Fatalf("clean: %+v", r2)
	}

	db := testDB(t)
	org, acc, msg := ids()
	ctx := store.WithOrganization(context.Background(), org)
	svc := &Service{
		DB: db, Scanner: s, Objects: testS3(t), // infected parts are never stored (P4.3 §4)
		SignKey:   []byte("clam-sign-key-32-bytes-!!!!!!!!"),
		PublicURL: "https://eb.example",
		Now:       func() time.Time { return time.Now().UTC() },
	}
	svc.init()
	_ = svc.store.ensure(ctx)
	raw := eicarMIME(eicar)
	svc.Raw = fakeRaw{body: raw} // the admin stream of an infected part comes from the raw source
	res, err := svc.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: raw, Scan: true, StoreS3: true,
	})
	if err != nil || len(res) != 1 || res[0].Scan != ScanInfected || res[0].URL != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := svc.SignedURL(ctx, acc, msg, res[0].ID); errs.CodeOf(err) != errs.CodeAttachmentInfected {
		t.Fatal(err)
	}
	// Authenticated stream still works for admin inspection.
	d, rc, err := svc.Open(ctx, acc, msg, res[0].ID)
	if err != nil || !d.Infected {
		t.Fatalf("%v %+v", err, d)
	}
	_ = rc.Close()
	if d.ObjectKey != "" {
		t.Fatalf("infected attachment was stored in S3: %s", d.ObjectKey)
	}
}

func eicarMIME(body []byte) []byte {
	// base64 of EICAR is fine; use 8bit for simplicity in test.
	return []byte("MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"hi\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"eicar.com\"\r\n" +
		"\r\n" +
		string(body) + "\r\n" +
		"--b--\r\n")
}

func TestIntegrationAuthenticatedSignedQuery(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	org, acc, msg := ids()
	ctx := store.WithOrganization(context.Background(), org)
	s := &Service{
		DB: db, Objects: objs,
		SignKey:   []byte("query-sign-key-32-bytes-!!!!!!!"),
		PublicURL: "https://eb.example",
		Now:       func() time.Time { return time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC) },
	}
	s.init()
	_ = s.store.ensure(ctx)
	res, err := s.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), StoreS3: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner}
	hctx := tenancy.WithCaller(ctx, caller)
	out, err := s.getAttachment(hctx, &getAttIn{AccountID: acc, MessageID: msg, AttachmentID: res[0].ID, URL: "signed"})
	if err != nil || out == nil {
		t.Fatal(err)
	}
	stream, err := s.getAttachment(hctx, &getAttIn{AccountID: acc, MessageID: msg, AttachmentID: res[0].ID})
	if err != nil || stream == nil {
		t.Fatal(err)
	}
}

func TestIntegrationMigrationAndStoreEdges(t *testing.T) {
	db := testDB(t)
	ctx := store.WithOrganization(context.Background(), "org_01habcdefghijklmnopqrstuvw")
	mig := Migration(27)
	if err := mig.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	ms := &mongoStore{DB: db}
	if err := ms.ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.get(ctx, "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw", "bad"); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
	if _, err := ms.get(ctx, "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw", "att_01habcdefghijklmnopqrstuvw"); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
	if _, err := ms.getByID(ctx, "org_01habcdefghijklmnopqrstuvw", "att_01habcdefghijklmnopqrstuvw"); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
	if _, err := ms.getByID(ctx, "org_01habcdefghijklmnopqrstuvw", "nope"); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
	got, err := ms.findByPart(ctx, "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw", "9")
	if err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	list, err := ms.listByMessage(ctx, "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw")
	if err != nil || len(list) != 0 {
		t.Fatal(err)
	}
	n, err := ms.deleteMessage(ctx, "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw")
	if err != nil || n != 0 {
		t.Fatal(err)
	}
}

func TestIntegrationModuleWithoutAPIRole(t *testing.T) {
	db := testDB(t)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, NodeID: "att-sync", Log: slog.Default()})
	app.Provide(a, db)
	m := &Module{SignKey: []byte("x"), PublicURL: "https://eb.example", Log: slog.Default()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(context.Background())
	if m.Service() == nil {
		t.Fatal("service")
	}
}

func TestIntegrationOpenBodyS3MissFallsThrough(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	org, acc, msg := ids()
	ctx := store.WithOrganization(context.Background(), org)
	s := &Service{
		DB: db, Objects: objs, Raw: fakeRaw{body: sampleMIME()},
		SignKey:   []byte("miss-sign-key-32-bytes-!!!!!!!!"),
		PublicURL: "https://eb.example",
		Now:       func() time.Time { return time.Now().UTC() },
	}
	s.init()
	_ = s.store.ensure(ctx)
	res, err := s.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), StoreS3: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := s.store.listByMessage(ctx, acc, msg)
	docs[0].ObjectKey = "attachments/missing/no/such"
	_ = s.store.upsert(ctx, docs[0])
	_, rc, err := s.Open(ctx, acc, msg, res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "pdf" {
		t.Fatalf("%q", body)
	}
}
