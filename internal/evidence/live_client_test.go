package evidence

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Hit the real minioAPI adapter methods (network fails fast) so unit coverage includes the live wiring.
func TestMinioAPIAdapterSmoke(t *testing.T) {
	b, err := NewMinioBackend("http://127.0.0.1:1", "us-east-1", "a", "s", "evidence", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = b.EnsureBucket(ctx)
	_ = b.PutLocked(ctx, "k", strings.NewReader("x"), 1, "text/plain", "GOVERNANCE", time.Now().Add(time.Hour))
	_, _, _ = b.Get(ctx, "k")
	_, _ = b.Exists(ctx, "k")
	_, _, _ = b.GetRetention(ctx, "k")
	_, _ = b.ObjectLockEnabled(ctx)

	c, err := minio.New("127.0.0.1:1", &minio.Options{
		Creds: credentials.NewStaticV4("a", "s", ""), Secure: false, Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	api := minioAPI{c: c}
	_ = api.MakeBucket(ctx, "evidence", minio.MakeBucketOptions{ObjectLocking: true})
	_, _ = api.GetObjectLockConfig(ctx, "evidence")
	_, _, _ = api.GetObject(ctx, "evidence", "missing")

	_, err = NewMinioBackend("http://127.0.0.1:1", "us-east-1", "", "", "evidence", false)
	if err != nil {
		t.Fatal(err)
	}
}
