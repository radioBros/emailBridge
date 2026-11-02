package evidence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/evidence"
)

func testStore(t *testing.T, lockOn bool) *evidence.Store {
	t.Helper()
	s, err := evidence.Open(evidence.Options{
		Backend:   evidence.NewMemoryBackend(lockOn),
		LockMode:  "GOVERNANCE",
		Retention: 48 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCheckAndArchiveRefuse(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	if err := s.Check(ctx); err == nil || errs.CodeOf(err) != errs.CodeEvidenceLockUnavailable {
		t.Fatalf("want lock unavailable, got %v", err)
	}
	_, err := s.Archive(ctx, evidence.Item{
		ID: "rcp_1", Organization: "org_1", Account: "acc_1", Raw: []byte("x"),
	})
	if err == nil || errs.CodeOf(err) != errs.CodeEvidenceLockUnavailable {
		t.Fatalf("archive refused: %v", err)
	}
}

func TestArchiveIdempotentAndConflict(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@b\r\n\r\nhello evidence")
	item := evidence.Item{
		ID: "rcp_abc", Organization: "org_a", Account: "acc_a",
		Raw: raw, ReceivedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		Type: "avvenuta-consegna", TransportID: "t1",
		Signature: map[string]any{"valid": true},
	}
	ref1, err := s.Archive(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	want := hex.EncodeToString(sum[:])
	if ref1.SHA256 != want || !strings.HasSuffix(ref1.Key, "/rcp_abc.eml") {
		t.Fatalf("%+v", ref1)
	}
	if ref1.LockedUntil == nil {
		t.Fatal("expected lockedUntil")
	}
	ref2, err := s.Archive(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if ref2.Key != ref1.Key || ref2.SHA256 != ref1.SHA256 {
		t.Fatalf("idempotent mismatch %+v %+v", ref1, ref2)
	}
	item.Raw = []byte("different")
	_, err = s.Archive(ctx, item)
	if err == nil || errs.CodeOf(err) != errs.CodeEvidenceHashConflict {
		t.Fatalf("want hash conflict, got %v", err)
	}
}

func TestVerify(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	_ = s.Check(ctx)
	raw := []byte("body")
	ref, err := s.Archive(ctx, evidence.Item{
		ID: "msg_1", Organization: "org", Account: "acc", Raw: raw,
		ReceivedAt: time.Now().UTC(), Type: "posta-certificata",
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	hexSum := hex.EncodeToString(sum[:])
	res, err := s.Verify(ctx, ref, hexSum)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Intact || !res.HashMatches || !res.Locked || res.TSA != nil {
		t.Fatalf("%+v", res)
	}
	res2, err := s.Verify(ctx, ref, strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	if res2.HashMatches {
		t.Fatal("expected hash mismatch vs expected")
	}
}

func TestObjectKey(t *testing.T) {
	at := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	k := evidence.ObjectKey("org_x", "acc_y", "rcp_z", at)
	if k != "evidence/org_x/acc_y/2026/03/rcp_z.eml" {
		t.Fatal(k)
	}
	if evidence.ManifestKeyFor(k) != "evidence/org_x/acc_y/2026/03/rcp_z.json" {
		t.Fatal(evidence.ManifestKeyFor(k))
	}
	if evidence.TSRKeyFor(k) != "evidence/org_x/acc_y/2026/03/rcp_z.tsr" {
		t.Fatal(evidence.TSRKeyFor(k))
	}
}

func TestOpenInvalidLockMode(t *testing.T) {
	_, err := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true), LockMode: "NOPE"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestArchiveValidation(t *testing.T) {
	s := testStore(t, true)
	_ = s.Check(context.Background())
	_, err := s.Archive(context.Background(), evidence.Item{})
	if err == nil || errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}
