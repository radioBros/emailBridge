package exports

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestGunzipConcatMultiAndEmpty(t *testing.T) {
	gzOne := func(s string) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return b.Bytes()
	}
	a, b := gzOne("aa\n"), gzOne("bb\n")
	got, err := gunzipConcat(append(append([]byte{}, a...), b...))
	if err != nil || string(got) != "aa\nbb\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := gunzipConcat([]byte{0xff, 0xff}); err == nil {
		t.Fatal("expected bad gzip error")
	}
	empty, err := gunzipConcat(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("%q %v", empty, err)
	}
}

func TestOpenBlobWithEncNoCipher(t *testing.T) {
	s := &Service{}
	e := &Export{
		State: StateSucceeded,
		Encryption: crypto.EncryptedString{
			KID: "k", Nonce: []byte{1}, CT: []byte{2},
		},
	}
	if _, err := s.openBlob(context.Background(), e); errs.CodeOf(err) != errs.CodeExportNotReady {
		t.Fatalf("%v", err)
	}
}

func TestRegisterHandlersNilJobs(t *testing.T) {
	(&Service{}).RegisterHandlers()
}

func TestOpenPartsEmptyList(t *testing.T) {
	s := &Service{}
	rc, err := s.openParts(context.Background(), bytes.Repeat([]byte{1}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil || len(b) == 0 {
		t.Fatalf("len=%d err=%v", len(b), err)
	}
}

func TestEmitAndAuditNilDeps(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	e := &Export{ID: "exp_x", OrganizationID: "org", AccountID: "acc"}
	s.recordAudit(ctx, "export.completed", e.ID, nil)
	if err := s.emitCompleted(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.emitFailed(ctx, e, "x", "y"); err != nil {
		t.Fatal(err)
	}
}
