//go:build integration

package exports

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationAPIHandlersAndFullExport(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, js := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 3)

	ctxBG := context.Background()
	var first index.Message
	if err := db.Collection(index.Messages).FindOne(ctxBG, bson.M{"accountId": acc}).Decode(&first); err == nil {
		key, _ := objstore.ArchiveKey(org, acc, first.ID, first.InternalDate)
		_ = objs.Put(ctxBG, key, bytes.NewReader([]byte("From: a@b\r\n\r\nbody")), 18, objstore.ObjectMeta{
			Organization: org, Account: acc, ContentType: "message/rfc822",
		})
		_, _ = db.Collection(index.Messages).UpdateOne(ctxBG, bson.M{"_id": first.ID}, bson.M{"$set": bson.M{"archiveRef": key}})
		attKey, _ := objstore.AttachmentKey(org, acc, store.NewID(store.PrefixAttachment))
		_ = objs.Put(ctxBG, attKey, bytes.NewReader([]byte("PDF")), 3, objstore.ObjectMeta{
			Organization: org, Account: acc, ContentType: "application/pdf",
		})
		_, _ = db.Collection(attachments.Collection).InsertOne(ctxBG, attachments.Doc{
			ID: store.NewID(store.PrefixAttachment), OrganizationID: org, AccountID: acc,
			MessageID: first.ID, PartID: "1", Filename: "a.pdf", ContentType: "application/pdf",
			Size: 3, ObjectKey: attKey, CreatedAt: time.Now().UTC(),
		})
	}

	ctx := asOwner(org)
	out, err := svc.createExport(ctx, &createIn{
		AccountPath: AccountPath{AccountID: acc},
		Body:        CreateInput{Include: Include{Raw: true, Attachments: true}},
	})
	if err != nil || out.Status != 202 || out.Location == "" {
		t.Fatalf("%+v %v", out, err)
	}
	expID := out.Body.Export.ID

	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": expID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.getExport(ctx, &ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: expID})
	if err != nil || got.Body.State != StateSucceeded {
		t.Fatalf("%+v %v", got, err)
	}

	list, err := svc.listExports(ctx, &listIn{AccountPath: AccountPath{AccountID: acc}, IncludeTotal: true, Sort: "createdAt"})
	if err != nil || len(list.Body.Data) < 1 {
		t.Fatalf("%+v %v", list, err)
	}

	stream, err := svc.downloadExport(ctx, &downloadIn{ExportPath: ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: expID}})
	if err != nil || stream == nil {
		t.Fatalf("%v", err)
	}
	suStream, err := svc.downloadExport(ctx, &downloadIn{
		ExportPath: ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: expID}, URL: "signed",
	})
	if err != nil || suStream == nil {
		t.Fatalf("signed: %v", err)
	}

	su, err := svc.SignedURL(ctx, acc, expID)
	if err != nil {
		t.Fatal(err)
	}
	tok := su.URL[len("http://localhost:8080/e/"):]
	signedStream, err := svc.signedDownload(ctx, &signedIn{Token: tok})
	if err != nil || signedStream == nil {
		t.Fatalf("%v", err)
	}

	js2 := &jobs.Service{DB: db, NodeID: "n2", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	svc2 := &Service{Repo: &Repo{DB: db}, Jobs: js2, Objects: objs, Cipher: testCipher(t, db),
		Events: &events.Writer{DB: db}, SkipFeatureGate: true, Log: slog.Default()}
	svc2.RegisterHandlers()
	if _, err = js2.RunNow(ctx, JobKind, org, map[string]string{}, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad payload: %v", err)
	}

	a2, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	co, err := svc.cancelExport(ctx, &ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: a2.Export.ID})
	if err != nil || co.Body.State != StateCancelled {
		t.Fatalf("%+v %v", co, err)
	}

	e := &Export{
		ID: store.NewID(store.PrefixExport), OrganizationID: org, AccountID: acc,
		State: StateExpired, CreatedAt: time.Now().UTC(),
	}
	_ = svc.Repo.insert(ctx, e)
	if _, err := svc.SignedURL(ctx, acc, e.ID); errs.CodeOf(err) != errs.CodeExportExpired {
		t.Fatalf("%v", err)
	}
	if _, _, err := svc.DownloadSigned(ctx, "bad.token"); err == nil {
		t.Fatal("expected bad token")
	}

	n, err := svc.EraseAccount(ctx, org, store.NewID(store.PrefixAccount))
	if err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}

	l2, err := svc.List(ctx, acc, ListFilter{Limit: 1, Sort: "-createdAt"})
	if err != nil || l2.NextCursor == nil || *l2.NextCursor == "" {
		t.Fatalf("need cursor: %+v %v", l2, err)
	}
	if _, err := svc.List(ctx, acc, ListFilter{Limit: 10, Cursor: *l2.NextCursor, Sort: "-createdAt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, acc, ListFilter{Page: 1, Limit: 10, Sort: "id"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, acc, ListFilter{Cursor: "!!!", Sort: "-createdAt"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestIntegrationExportWithoutObjects(t *testing.T) {
	db := testDB(t)
	svc, js := newSvc(t, db, nil)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 1)
	ctx := asOwner(org)
	a, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, acc, a.Export.ID)
	if got.State != StateSucceeded {
		t.Fatalf("%+v", got)
	}
	if _, _, err := svc.OpenDownload(ctx, acc, a.Export.ID); err == nil {
		t.Fatal("expected download error without objects")
	}
}

func TestIntegrationCancelDuringJob(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, js := newSvc(t, db, objs)
	svc.BatchSize = 1
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 4)
	ctx := asOwner(org)
	a, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_, _ = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, func() bool {
		n++
		return n > 2
	})
	got, err := svc.Get(ctx, acc, a.Export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCancelled && got.State != StateSucceeded {
		t.Fatalf("state %s", got.State)
	}
}

func TestIntegrationViewerForbidden(t *testing.T) {
	db := testDB(t)
	svc, _ := newSvc(t, db, testS3(t))
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if _, err := svc.Create(ctx, acc, CreateInput{}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationRetentionDutyLoop(t *testing.T) {
	db := testDB(t)
	svc, _ := newSvc(t, db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RetentionDuty(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention duty did not exit")
	}
}

func TestIntegrationMaxConcurrentAndAPIKey(t *testing.T) {
	db := testDB(t)
	svc, _ := newSvc(t, db, nil)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: org, Scopes: []string{"accounts:write", "accounts:read"},
	})
	for i := 0; i < maxConcurrent; i++ {
		if _, err := svc.Create(ctx, acc, CreateInput{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, acc, CreateInput{}); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("max concurrent: %v", err)
	}
}

func TestIntegrationIdempotentRerunAndFail(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, js := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 2)
	ctx := asOwner(org)
	a, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Idempotent re-run
	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := svc.Get(ctx, acc, a2.Export.ID)
	cause := errs.New(errs.CodeInternalError, "forced failure")
	if err := svc.failJob(ctx, e2, cause); err == nil {
		t.Fatal("failJob should return cause")
	}
	got, _ := svc.Get(ctx, acc, a2.Export.ID)
	if got.State != StateFailed {
		t.Fatalf("want failed, got %s", got.State)
	}

	mig := Migration(28)
	if mig.Name == "" || mig.Number != 28 {
		t.Fatal(mig)
	}
}
