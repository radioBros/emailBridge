package exports

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestMapTokenErrDefault(t *testing.T) {
	err := mapTokenErr(errors.New("other"))
	if errs.CodeOf(err) != errs.CodeSignedURLInvalid {
		t.Fatalf("%v", err)
	}
}

func TestOpenAESGCMBadMagic(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	blob := append([]byte("XXXX"), make([]byte, 20)...)
	_, err := openAESGCM(key, blob)
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("%v", err)
	}
}

func TestDecodeErrorEmpty(t *testing.T) {
	e := &Export{}
	e.decodeError()
	e.ErrorText = `{not json`
	e.decodeError()
	if e.Error != nil {
		t.Fatal("bad json should leave Error nil")
	}
}

func TestViewOfAndHelpers(t *testing.T) {
	v := viewOf(&Export{ID: "exp_1", State: StateSucceeded, MessageCount: 2, ByteSize: 9})
	if v["id"] != "exp_1" {
		t.Fatalf("%v", v)
	}
	if encodeB64(nil) != "" || encodeB64([]byte("ab")) == "" {
		t.Fatal("b64")
	}
	b := mustJSON(map[string]int{"a": 1})
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("%q", b)
	}
}

func TestServiceDefaults(t *testing.T) {
	s := &Service{}
	if s.now().IsZero() || s.log() == nil || s.batch() != defaultBatch || s.registry() == nil {
		t.Fatal("defaults")
	}
	s.BatchSize = 7
	if s.batch() != 7 {
		t.Fatal("batch override")
	}
	s.Now = func() time.Time { return time.Unix(1, 0).UTC() }
	if !s.now().Equal(time.Unix(1, 0).UTC()) {
		t.Fatal("now override")
	}
}

func TestCreateValidation(t *testing.T) {
	s := &Service{SkipFeatureGate: true, Repo: &Repo{}}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	since := time.Now()
	before := since.Add(-time.Hour)
	_, err := s.Create(ctx, store.NewID(store.PrefixAccount), CreateInput{Since: &since, Before: &before})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("since/before: %v", err)
	}
	_, err = s.Create(ctx, store.NewID(store.PrefixAccount), CreateInput{FolderIDs: []string{"bad"}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("folder: %v", err)
	}
	_, err = s.Create(ctx, "not-an-account", CreateInput{})
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("account id: %v", err)
	}
}

func TestGetBadID(t *testing.T) {
	s := &Service{SkipFeatureGate: true}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	if _, err := s.Get(ctx, store.NewID(store.PrefixAccount), "bad"); errs.CodeOf(err) != errs.CodeExportNotFound {
		t.Fatalf("%v", err)
	}
}

func TestAPIHandlersUnauth(t *testing.T) {
	s := &Service{SkipFeatureGate: true}
	ctx := context.Background()
	acc := store.NewID(store.PrefixAccount)
	if _, err := s.listExports(ctx, &listIn{AccountPath: AccountPath{AccountID: acc}}); err == nil {
		t.Fatal("list unauth")
	}
	if _, err := s.createExport(ctx, &createIn{AccountPath: AccountPath{AccountID: acc}}); err == nil {
		t.Fatal("create unauth")
	}
	if _, err := s.getExport(ctx, &ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: store.NewID(store.PrefixExport)}); err == nil {
		t.Fatal("get unauth")
	}
	if _, err := s.cancelExport(ctx, &ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: store.NewID(store.PrefixExport)}); err == nil {
		t.Fatal("cancel unauth")
	}
	if _, err := s.downloadExport(ctx, &downloadIn{ExportPath: ExportPath{AccountPath: AccountPath{AccountID: acc}, ExportID: store.NewID(store.PrefixExport)}}); err == nil {
		t.Fatal("download unauth")
	}
}

func TestOpenBlobStates(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if _, err := s.openBlob(ctx, &Export{State: StateExpired}); errs.CodeOf(err) != errs.CodeExportExpired {
		t.Fatalf("%v", err)
	}
	if _, err := s.openBlob(ctx, &Export{State: StateQueued}); errs.CodeOf(err) != errs.CodeExportNotReady {
		t.Fatalf("%v", err)
	}
	if _, err := s.openBlob(ctx, &Export{State: StateSucceeded}); errs.CodeOf(err) != errs.CodeExportNotReady {
		t.Fatalf("%v", err)
	}
}

func TestReadAll(t *testing.T) {
	b, err := readAll(io.NopCloser(bytes.NewReader([]byte("hi"))))
	if err != nil || string(b) != "hi" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestMessageFilterCheckpoint(t *testing.T) {
	s := &Service{}
	e := &Export{
		AccountID: "acc_1", OrganizationID: "org_1",
		Filters: Filters{FolderIDs: []string{"mbx_1"}, Since: ptrTime(time.Unix(1, 0)), Before: ptrTime(time.Unix(9, 0))},
	}
	f := s.messageFilter(e, &Checkpoint{MailboxID: "mbx_1", UID: 3})
	if f["accountId"] != "acc_1" || f["$or"] == nil {
		t.Fatalf("%v", f)
	}
}

func TestCursorFilterBadCreatedAt(t *testing.T) {
	_, err := cursorFilter(cursorPayload{ID: "x", Key: "not-a-time"}, "createdAt", -1)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	// non-string key falls through
	if _, err := cursorFilter(cursorPayload{ID: "x", Key: float64(1)}, "createdAt", 1); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyTokenMoreEdges(t *testing.T) {
	key := []byte("signing-key-32-bytes-long!!!!!!")
	tok, _ := Sign(key, "org", "exp", time.Unix(9999999999, 0))
	_ = tok
	if _, _, err := VerifyToken(key, "v1.onlyonepart", time.Now()); !errors.Is(err, errBadToken) {
		t.Fatalf("%v", err)
	}
	if _, _, err := VerifyToken(key, "abc.!!!", time.Now()); !errors.Is(err, errBadToken) {
		t.Fatalf("%v", err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
