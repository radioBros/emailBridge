package attachments

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fakeRaw struct{ body []byte }

func (f fakeRaw) OpenRaw(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

type fakeFetcher struct {
	body []byte
	err  error
}

func (f fakeFetcher) FetchPart(context.Context, string, string, string) (io.ReadCloser, PartInfo, error) {
	if f.err != nil {
		return nil, PartInfo{}, f.err
	}
	return io.NopCloser(bytes.NewReader(f.body)), PartInfo{ContentType: "application/pdf", Filename: "a.pdf"}, nil
}

func testSvc(t *testing.T) *Service {
	t.Helper()
	s := &Service{
		SignKey:   []byte("unit-test-sign-key-32-bytes!!!!"),
		PublicURL: "https://eb.example",
		SignedTTL: 10 * time.Minute,
		Raw:       fakeRaw{body: sampleMIME()},
		Now:       func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) },
		store:     newMemDocs(),
	}
	s.init()
	return s
}

func TestIngestOpenSignedAndErase(t *testing.T) {
	s := testSvc(t)
	ctx := store.WithOrganization(context.Background(), "org_01habcdefghijklmnopqrstuvw")
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"

	res, err := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), Scan: true})
	if err != nil || len(res) != 1 || res[0].ID == "" || res[0].Scan != ScanSkipped {
		t.Fatalf("%+v %v", res, err)
	}
	// Infected path via fake scanner
	s.Scanner = &Scanner{Addr: "tcp://127.0.0.1:1", Timeout: time.Millisecond}
	res2, err := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), Scan: true})
	if err != nil || res2[0].Scan != ScanError {
		t.Fatalf("%+v %v", res2, err)
	}

	d, rc, err := s.Open(ctx, acc, msg, res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "pdf" || d.Filename != "a.pdf" {
		t.Fatalf("%q %+v", body, d)
	}

	// Fetcher wins when set
	s.Fetcher = fakeFetcher{body: []byte("from-imap")}
	_, rc, err = s.Open(ctx, acc, msg, res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "from-imap" {
		t.Fatalf("%q", body)
	}
	s.Fetcher = nil

	if _, err := s.SignedURL(ctx, acc, msg, res[0].ID); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("%v", err)
	}

	// Mark as stored for signed URL
	docs, _ := s.store.listByMessage(ctx, acc, msg)
	docs[0].ObjectKey = "attachments/org/acc/att"
	docs[0].Infected = false
	_ = s.store.upsert(ctx, docs[0])
	su, err := s.SignedURL(ctx, acc, msg, docs[0].ID)
	if err != nil || !strings.HasPrefix(su.URL, "https://eb.example/a/") {
		t.Fatalf("%+v %v", su, err)
	}
	tok := strings.TrimPrefix(su.URL, "https://eb.example/a/")
	d2, rc, err := s.OpenSigned(ctx, tok)
	if err != nil || d2.ID != docs[0].ID {
		t.Fatalf("%v %+v", err, d2)
	}
	_ = rc.Close()

	docs[0].Infected = true
	_ = s.store.upsert(ctx, docs[0])
	if _, err := s.SignedURL(ctx, acc, msg, docs[0].ID); errs.CodeOf(err) != errs.CodeAttachmentInfected {
		t.Fatal(err)
	}
	if _, _, err := s.OpenSigned(ctx, tok); errs.CodeOf(err) != errs.CodeAttachmentInfected {
		t.Fatal(err)
	}

	n, err := s.EraseMessage(ctx, acc, msg)
	if err != nil || n < 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, _, err := s.Open(ctx, acc, msg, res[0].ID); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
}

func TestGetAttachmentHandlerAndSignedRoute(t *testing.T) {
	s := testSvc(t)
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	ctx := store.WithOrganization(context.Background(), org)
	res, err := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME()})
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := s.store.listByMessage(ctx, acc, msg)
	docs[0].ObjectKey = "k"
	_ = s.store.upsert(ctx, docs[0])

	caller := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner}
	hctx := tenancy.WithCaller(context.Background(), caller)

	out, err := s.getAttachment(hctx, &getAttIn{AccountID: acc, MessageID: msg, AttachmentID: res[0].ID})
	if err != nil || out == nil {
		t.Fatal(err)
	}
	hdr := http.Header{}
	docs[0].Scan = ScanClean
	docs[0].ContentType = ""
	setDocHeaders(hdr.Set, docs[0])
	if hdr.Get("Content-Type") != "application/octet-stream" || hdr.Get("EB-Scan") != ScanClean {
		t.Fatal(hdr)
	}
	su, err := s.getAttachment(hctx, &getAttIn{AccountID: acc, MessageID: msg, AttachmentID: res[0].ID, URL: "signed"})
	if err != nil || su == nil {
		t.Fatal(err)
	}

	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	s.Register(srv)
	tok, _ := Sign(s.SignKey, org, docs[0].ID, s.Now().Add(time.Hour))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/a/"+tok, nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pdf") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/a/bad.token", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestModuleMigrationName(t *testing.T) {
	m := &Module{}
	if m.Name() != "attachments" || m.Roles() != nil {
		t.Fatal(m.Name())
	}
	mig := Migration(27)
	if mig.Number != 27 || mig.Name == "" {
		t.Fatal(mig)
	}
	_ = m.Stop(context.Background())
}

func TestIngestValidation(t *testing.T) {
	s := testSvc(t)
	if _, err := s.Ingest(context.Background(), IngestInput{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}
