//go:build integration

package evidence_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/test/containers"
)

// SeaweedFS (the shared S3 testcontainer) does not support Object Lock.
// Object Lock paths are covered by MemoryBackend unit tests. This suite checks
// that MinioBackend can put/get against the real S3 endpoint without lock headers.

func TestIntegrationMinioPutGet(t *testing.T) {
	ep, ak, sk := containers.S3(t)
	b, err := evidence.NewMinioBackend("http://"+ep, "us-east-1", ak, sk, "evidence-test", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	// Object Lock is expected to be unavailable on SeaweedFS.
	ok, err := b.ObjectLockEnabled(ctx)
	if err == nil && ok {
		t.Log("unexpected: Object Lock enabled on test S3 — running locked put")
	} else {
		t.Logf("Object Lock not available on test S3 (ok=%v err=%v); unlocked put only", ok, err)
	}

	body := []byte("From: a@b\r\n\r\nintegration")
	key := "evidence/org_i/acc_i/2026/10/rcp_int.eml"
	if err := b.PutLocked(ctx, key, bytes.NewReader(body), int64(len(body)), "message/rfc822", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	rc, size, err := b.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, body) || size != int64(len(body)) {
		t.Fatalf("get mismatch %q size=%d", got, size)
	}
	sum := sha256.Sum256(body)
	_ = hex.EncodeToString(sum[:])

	exists, err := b.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("exists %v %v", exists, err)
	}
}

func TestIntegrationCheckFailsWithoutLock(t *testing.T) {
	ep, ak, sk := containers.S3(t)
	b, err := evidence.NewMinioBackend("http://"+ep, "us-east-1", ak, sk, "evidence-nolock", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = b.EnsureBucket(ctx)
	s, err := evidence.Open(evidence.Options{Backend: b, LockMode: "GOVERNANCE"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Check(ctx)
	// SeaweedFS: GetObjectLockConfig typically errors or returns not Enabled.
	if err == nil && s.Ready() {
		t.Skip("test S3 has Object Lock; Check succeeded — fine")
	}
	if err == nil {
		t.Fatal("expected Check error when Object Lock is off")
	}
}
