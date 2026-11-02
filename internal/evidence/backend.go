package evidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Backend is the narrow object API used by Store (Object Lock put + get + retention).
// Production uses MinioBackend; tests may inject MemoryBackend (SeaweedFS has no Object Lock).
type Backend interface {
	PutLocked(ctx context.Context, key string, r io.Reader, size int64, contentType string, mode string, until time.Time) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Exists(ctx context.Context, key string) (bool, error)
	GetRetention(ctx context.Context, key string) (locked bool, until *time.Time, err error)
	ObjectLockEnabled(ctx context.Context) (bool, error)
}

// objectAPI is the minio subset MinioBackend needs (fakeable in unit tests).
type objectAPI interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	MakeBucket(ctx context.Context, bucket string, opts minio.MakeBucketOptions) error
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error)
	StatObject(ctx context.Context, bucket, key string) error
	GetObjectRetention(ctx context.Context, bucket, key string) (mode *minio.RetentionMode, until *time.Time, err error)
	GetObjectLockConfig(ctx context.Context, bucket string) (objectLock string, err error)
}

type minioAPI struct{ c *minio.Client }

func (m minioAPI) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return m.c.BucketExists(ctx, bucket)
}
func (m minioAPI) MakeBucket(ctx context.Context, bucket string, opts minio.MakeBucketOptions) error {
	return m.c.MakeBucket(ctx, bucket, opts)
}
func (m minioAPI) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
	_, err := m.c.PutObject(ctx, bucket, key, r, size, opts)
	return err
}
func (m minioAPI) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error) {
	obj, err := m.c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, err
	}
	return obj, st.Size, nil
}
func (m minioAPI) StatObject(ctx context.Context, bucket, key string) error {
	_, err := m.c.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	return err
}
func (m minioAPI) GetObjectRetention(ctx context.Context, bucket, key string) (*minio.RetentionMode, *time.Time, error) {
	return m.c.GetObjectRetention(ctx, bucket, key, "")
}
func (m minioAPI) GetObjectLockConfig(ctx context.Context, bucket string) (string, error) {
	enabled, _, _, _, err := m.c.GetObjectLockConfig(ctx, bucket)
	return enabled, err
}

// MinioBackend talks to an S3-compatible endpoint with Object Lock headers on put.
type MinioBackend struct {
	client objectAPI
	bucket string
	region string
}

// NewMinioBackend builds a client from connection options (same shape as objstore).
func NewMinioBackend(endpoint, region, accessKey, secretKey, bucket string, pathStyle bool) (*MinioBackend, error) {
	if endpoint == "" || bucket == "" {
		return nil, errors.New("evidence: Endpoint and Bucket are required")
	}
	if region == "" {
		region = "us-east-1"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		if !strings.Contains(endpoint, "://") {
			u, err = url.Parse("http://" + endpoint)
		}
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("evidence: bad endpoint %q: %w", endpoint, err)
		}
	}
	secure := u.Scheme == "https"
	var creds *credentials.Credentials
	if accessKey == "" && secretKey == "" {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.IAM{Endpoint: ""},
		})
	} else {
		creds = credentials.NewStaticV4(accessKey, secretKey, "")
	}
	lookup := minio.BucketLookupAuto
	if pathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds: creds, Secure: secure, Region: region, BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: client: %w", err)
	}
	return &MinioBackend{client: minioAPI{c: client}, bucket: bucket, region: region}, nil
}

// EnsureBucket creates the bucket with Object Lock when missing.
func (b *MinioBackend) EnsureBucket(ctx context.Context) error {
	ok, err := b.client.BucketExists(ctx, b.bucket)
	if err != nil {
		return fmt.Errorf("evidence: BucketExists: %w", err)
	}
	if ok {
		return nil
	}
	err = b.client.MakeBucket(ctx, b.bucket, minio.MakeBucketOptions{Region: b.region, ObjectLocking: true})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == minio.BucketAlreadyOwnedByYou || resp.Code == minio.BucketAlreadyExists {
			return nil
		}
		return fmt.Errorf("evidence: MakeBucket: %w", err)
	}
	return nil
}

func (b *MinioBackend) PutLocked(ctx context.Context, key string, r io.Reader, size int64, contentType string, mode string, until time.Time) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	opts := minio.PutObjectOptions{ContentType: contentType}
	if mode != "" && !until.IsZero() {
		opts.Mode = minio.RetentionMode(strings.ToUpper(mode))
		opts.RetainUntilDate = until.UTC()
	}
	if err := b.client.PutObject(ctx, b.bucket, key, r, size, opts); err != nil {
		return fmt.Errorf("evidence: PutLocked: %w", err)
	}
	return nil
}

func (b *MinioBackend) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	rc, size, err := b.client.GetObject(ctx, b.bucket, key)
	if err != nil {
		return nil, 0, mapMinioErr(err)
	}
	return rc, size, nil
}

func (b *MinioBackend) Exists(ctx context.Context, key string) (bool, error) {
	err := b.client.StatObject(ctx, b.bucket, key)
	if err != nil {
		if errors.Is(mapMinioErr(err), ErrNotFound) {
			return false, nil
		}
		return false, mapMinioErr(err)
	}
	return true, nil
}

func (b *MinioBackend) GetRetention(ctx context.Context, key string) (bool, *time.Time, error) {
	mode, until, err := b.client.GetObjectRetention(ctx, b.bucket, key)
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchObjectLockConfiguration" || resp.Code == minio.NoSuchKey || resp.StatusCode == http.StatusNotFound {
			return false, nil, nil
		}
		return false, nil, err
	}
	if mode == nil || until == nil || until.IsZero() {
		return false, nil, nil
	}
	u := until.UTC()
	return true, &u, nil
}

func (b *MinioBackend) ObjectLockEnabled(ctx context.Context) (bool, error) {
	ok, err := b.client.BucketExists(ctx, b.bucket)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("evidence: bucket %q does not exist", b.bucket)
	}
	enabled, err := b.client.GetObjectLockConfig(ctx, b.bucket)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(enabled, "Enabled"), nil
}

func mapMinioErr(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == minio.NoSuchKey || resp.Code == "NotFound" || resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return err
}

// MemoryBackend is an in-memory Backend with simulated Object Lock (for unit tests;
// the SeaweedFS testcontainer does not support Object Lock).
type MemoryBackend struct {
	mu      sync.Mutex
	objects map[string]memObj
	lockOn  bool
}

type memObj struct {
	body  []byte
	ct    string
	mode  string
	until time.Time
}

// NewMemoryBackend returns an empty memory store; Object Lock reports enabled when lockOn is true.
func NewMemoryBackend(lockOn bool) *MemoryBackend {
	return &MemoryBackend{objects: map[string]memObj{}, lockOn: lockOn}
}

func (m *MemoryBackend) PutLocked(_ context.Context, key string, r io.Reader, size int64, contentType string, mode string, until time.Time) error {
	var body []byte
	var err error
	if r != nil {
		body, err = io.ReadAll(r)
		if err != nil {
			return err
		}
	}
	if size >= 0 && int64(len(body)) > size {
		body = body[:size]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObj{body: append([]byte(nil), body...), ct: contentType, mode: mode, until: until.UTC()}
	return nil
}

func (m *MemoryBackend) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o.body)), int64(len(o.body)), nil
}

func (m *MemoryBackend) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok, nil
}

func (m *MemoryBackend) GetRetention(_ context.Context, key string) (bool, *time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return false, nil, ErrNotFound
	}
	if o.mode == "" || o.until.IsZero() {
		return false, nil, nil
	}
	u := o.until
	return true, &u, nil
}

func (m *MemoryBackend) ObjectLockEnabled(context.Context) (bool, error) {
	return m.lockOn, nil
}
