package evidence

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

type fakeAPI struct {
	exists      bool
	lockEnabled string
	objs        map[string][]byte
	retention   map[string]time.Time
	putErr      error
	statErr     error
	getErr      error
	bucketErr   error
	makeErr     error
	lockErr     error
	retErr      error
	retMode     *minio.RetentionMode
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{objs: map[string][]byte{}, retention: map[string]time.Time{}, exists: true, lockEnabled: "Enabled"}
}

func (f *fakeAPI) BucketExists(context.Context, string) (bool, error) {
	if f.bucketErr != nil {
		return false, f.bucketErr
	}
	return f.exists, nil
}
func (f *fakeAPI) MakeBucket(context.Context, string, minio.MakeBucketOptions) error {
	return f.makeErr
}
func (f *fakeAPI) PutObject(_ context.Context, _, key string, r io.Reader, _ int64, opts minio.PutObjectOptions) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, _ := io.ReadAll(r)
	f.objs[key] = b
	if !opts.RetainUntilDate.IsZero() {
		f.retention[key] = opts.RetainUntilDate
	}
	return nil
}
func (f *fakeAPI) GetObject(_ context.Context, _, key string) (io.ReadCloser, int64, error) {
	if f.getErr != nil {
		return nil, 0, f.getErr
	}
	b, ok := f.objs[key]
	if !ok {
		return nil, 0, minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (f *fakeAPI) StatObject(_ context.Context, _, key string) error {
	if f.statErr != nil {
		return f.statErr
	}
	if _, ok := f.objs[key]; !ok {
		return minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
	}
	return nil
}
func (f *fakeAPI) GetObjectRetention(_ context.Context, _, key string) (*minio.RetentionMode, *time.Time, error) {
	if f.retErr != nil {
		return nil, nil, f.retErr
	}
	if f.retMode != nil {
		u := f.retention[key]
		if u.IsZero() {
			u = time.Now().Add(time.Hour)
		}
		return f.retMode, &u, nil
	}
	u, ok := f.retention[key]
	if !ok {
		return nil, nil, minio.ErrorResponse{Code: "NoSuchObjectLockConfiguration", StatusCode: 404}
	}
	m := minio.Governance
	return &m, &u, nil
}
func (f *fakeAPI) GetObjectLockConfig(context.Context, string) (string, error) {
	if f.lockErr != nil {
		return "", f.lockErr
	}
	return f.lockEnabled, nil
}

func TestMinioBackendWithFake(t *testing.T) {
	f := newFakeAPI()
	b := &MinioBackend{client: f, bucket: "ev", region: "us-east-1"}
	ctx := context.Background()
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	f.exists = false
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	f.makeErr = minio.ErrorResponse{Code: minio.BucketAlreadyOwnedByYou}
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	f.makeErr = errors.New("boom")
	if err := b.EnsureBucket(ctx); err == nil {
		t.Fatal("expected make error")
	}
	f.makeErr = nil
	f.exists = true

	until := time.Now().UTC().Add(48 * time.Hour)
	if err := b.PutLocked(ctx, "k", strings.NewReader("hi"), 2, "", "GOVERNANCE", until); err != nil {
		t.Fatal(err)
	}
	rc, size, err := b.Get(ctx, "k")
	if err != nil || size != 2 {
		t.Fatalf("%v %d", err, size)
	}
	_ = rc.Close()
	ok, err := b.Exists(ctx, "k")
	if !ok || err != nil {
		t.Fatal(err)
	}
	ok, err = b.Exists(ctx, "missing")
	if ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	locked, u, err := b.GetRetention(ctx, "k")
	if err != nil || !locked || u == nil {
		t.Fatalf("%v %v %v", locked, u, err)
	}
	on, err := b.ObjectLockEnabled(ctx)
	if err != nil || !on {
		t.Fatalf("%v %v", on, err)
	}
	f.lockEnabled = "Disabled"
	on, err = b.ObjectLockEnabled(ctx)
	if err != nil || on {
		t.Fatalf("%v %v", on, err)
	}
	f.exists = false
	_, err = b.ObjectLockEnabled(ctx)
	if err == nil {
		t.Fatal("expected missing bucket")
	}
}

func TestMinioBackendErrors(t *testing.T) {
	f := newFakeAPI()
	b := &MinioBackend{client: f, bucket: "ev", region: "us-east-1"}
	ctx := context.Background()
	f.putErr = errors.New("put fail")
	if err := b.PutLocked(ctx, "k", strings.NewReader("x"), 1, "text/plain", "", time.Time{}); err == nil {
		t.Fatal("expected put error")
	}
	f.putErr = nil
	f.getErr = minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: 404}
	_, _, err := b.Get(ctx, "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	f.getErr = errors.New("other")
	_, _, err = b.Get(ctx, "k")
	if err == nil {
		t.Fatal("expected get error")
	}
	f.statErr = errors.New("stat")
	_, err = b.Exists(ctx, "k")
	if err == nil {
		t.Fatal("expected stat error")
	}
	f.statErr = nil
	f.retErr = minio.ErrorResponse{Code: "NoSuchObjectLockConfiguration", StatusCode: 404}
	locked, _, err := b.GetRetention(ctx, "k")
	if err != nil || locked {
		t.Fatalf("%v %v", locked, err)
	}
	f.retErr = errors.New("ret")
	_, _, err = b.GetRetention(ctx, "k")
	if err == nil {
		t.Fatal("expected ret error")
	}
	f.retErr = nil
	f.retMode = ptrMode(minio.Governance)
	// empty until from fake when retention map empty — GetRetention uses Now+1h
	f.retention["k"] = time.Time{}
	locked, u, err := b.GetRetention(ctx, "k")
	if err != nil || !locked || u == nil {
		t.Fatalf("%v %v %v", locked, u, err)
	}
	f.lockErr = errors.New("lock")
	f.exists = true
	_, err = b.ObjectLockEnabled(ctx)
	if err == nil {
		t.Fatal("expected lock err")
	}
	f.bucketErr = errors.New("be")
	_, err = b.ObjectLockEnabled(ctx)
	if err == nil {
		t.Fatal("expected bucket err")
	}
	if mapMinioErr(nil) != nil {
		t.Fatal()
	}
}

func ptrMode(m minio.RetentionMode) *minio.RetentionMode { return &m }

func TestNewMinioBackendValidation(t *testing.T) {
	_, err := NewMinioBackend("", "r", "a", "s", "b", true)
	if err == nil {
		t.Fatal()
	}
	_, err = NewMinioBackend("http://127.0.0.1:9", "", "a", "s", "b", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewMinioBackend("127.0.0.1:9", "us-east-1", "", "", "b", false)
	if err != nil {
		t.Fatal(err)
	}
}
