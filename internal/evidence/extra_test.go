package evidence_test

import (
	"context"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/jobs"
)

func TestModuleNameRoles(t *testing.T) {
	m := &evidence.Module{}
	if m.Name() != "evidence" || m.Roles() != nil {
		t.Fatalf("%s %v", m.Name(), m.Roles())
	}
}

func TestLoadAnchorsFile(t *testing.T) {
	fx := startFakeTSA(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fx.caCert.Raw})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	certs, err := evidence.LoadAnchorsFile(path)
	if err != nil || len(certs) != 1 {
		t.Fatalf("%v %d", err, len(certs))
	}
	certs2, err := evidence.LoadAnchorsFile(string(pemBytes))
	if err != nil || len(certs2) != 1 {
		t.Fatalf("inline: %v", err)
	}
	n, err := evidence.LoadAnchorsFile("")
	if err != nil || n != nil {
		t.Fatalf("empty: %v %v", err, n)
	}
	_, err = evidence.LoadAnchorsFile(filepath.Join(dir, "missing.pem"))
	if err == nil {
		t.Fatal("expected read error")
	}
}

func TestOpenRequiresEndpoint(t *testing.T) {
	_, err := evidence.Open(evidence.Options{Bucket: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegisterHandlersNil(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	s.RegisterHandlers(nil)
	js := &jobs.Service{}
	s.RegisterHandlers(js)
}

func TestNewTSARetryPayloadZeroTime(t *testing.T) {
	p := evidence.NewTSARetryPayload(evidence.Ref{Key: "k", SHA256: "s"}, evidence.Item{TSAURL: "u"}, time.Time{})
	if p.FirstAttemptAt.IsZero() {
		t.Fatal("expected now")
	}
}

func TestArchiveEmptyRaw(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	_ = s.Check(context.Background())
	_, err := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_e", Organization: "o", Account: "a", Raw: nil,
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestVerifyMissing(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	_ = s.Check(context.Background())
	_, err := s.Verify(context.Background(), evidence.Ref{}, "")
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	_, err = s.Verify(context.Background(), evidence.Ref{Key: "evidence/o/a/2026/01/missing.eml"}, "")
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("%v", err)
	}
}

func TestMemoryRetentionUnlocked(t *testing.T) {
	b := evidence.NewMemoryBackend(true)
	ctx := context.Background()
	_ = b.PutLocked(ctx, "k", nil, 0, "application/octet-stream", "", time.Time{})
	locked, until, err := b.GetRetention(ctx, "k")
	if err != nil || locked || until != nil {
		t.Fatalf("%v %v %v", locked, until, err)
	}
	_, _, err = b.GetRetention(ctx, "missing")
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestStampValidation(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	_, _, err := s.Stamp(context.Background(), "zz", "http://x")
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	_, _, err = s.Stamp(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestHandleTSARetryBadPayload(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	_, err := s.HandleTSARetry(context.Background(), &jobs.Job{Payload: []byte(`{`)}, nil)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	_, err = s.HandleTSARetry(context.Background(), &jobs.Job{Payload: []byte(`{}`)}, nil)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestModuleCheckFailsLogs(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n", Log: quiet})
	m := &evidence.Module{
		Options: evidence.Options{Backend: evidence.NewMemoryBackend(false)},
		Log:     quiet,
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Store().Ready() {
		t.Fatal("should not be ready")
	}
}

func TestComplianceMode(t *testing.T) {
	s, err := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true), LockMode: "compliance"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Check(context.Background())
	_, err = s.Archive(context.Background(), evidence.Item{
		ID: "rcp_c", Organization: "o", Account: "a", Raw: []byte("z"),
		ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadAnchorsNonCertPEM(t *testing.T) {
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	_, err := evidence.LoadAnchorsPEM(block)
	if err == nil {
		t.Fatal("expected error")
	}
}
