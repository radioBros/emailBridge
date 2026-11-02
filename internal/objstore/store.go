// Package objstore is EmailBridge's S3-compatible object storage (S1 §7, S9 §2.3, S15 §1).
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	metaOrg     = "eb-org"
	metaAccount = "eb-account"
	maxPresign  = 7 * 24 * time.Hour
)

// ErrNotFound is returned when an object key does not exist.
var ErrNotFound = errors.New("objstore: not found")

// Options configure a Store (EB_S3_*).
type Options struct {
	Endpoint       string
	Region         string
	AccessKey      string
	SecretKey      string
	Bucket         string
	PathStyle      bool
	EvidenceBucket string
}

// ObjectMeta is written as x-amz-meta-eb-org / x-amz-meta-eb-account (S1 §7).
type ObjectMeta struct {
	Organization string
	Account      string
	ContentType  string
}

// ObjectInfo is a subset of object metadata returned by Get/Stat/List.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	Organization string
	Account      string
	ETag         string
	LastModified time.Time
}

// Store wraps a minio client and a bucket.
type Store struct {
	client *minio.Client
	bucket string
	opts   Options
}

// Open builds a Store from Options. It does not create the bucket (see EnsureBucket).
func Open(o Options) (*Store, error) {
	if o.Endpoint == "" || o.Bucket == "" {
		return nil, errors.New("objstore: Endpoint and Bucket are required")
	}
	if o.Region == "" {
		o.Region = "us-east-1"
	}
	u, err := url.Parse(o.Endpoint)
	if err != nil || u.Host == "" {
		// allow bare host:port
		if !strings.Contains(o.Endpoint, "://") {
			u, err = url.Parse("http://" + o.Endpoint)
		}
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("objstore: bad endpoint %q: %w", o.Endpoint, err)
		}
	}
	secure := u.Scheme == "https"
	host := u.Host
	var creds *credentials.Credentials
	if o.AccessKey == "" && o.SecretKey == "" {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.IAM{Endpoint: ""},
		})
	} else {
		creds = credentials.NewStaticV4(o.AccessKey, o.SecretKey, "")
	}
	lookup := minio.BucketLookupAuto
	if o.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        creds,
		Secure:       secure,
		Region:       o.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("objstore: client: %w", err)
	}
	return &Store{client: client, bucket: o.Bucket, opts: o}, nil
}

// EnsureBucket creates the bucket if missing (idempotent; tolerates already-owned).
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("objstore: BucketExists: %w", err)
	}
	if exists {
		return nil
	}
	err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.opts.Region})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == minio.BucketAlreadyOwnedByYou || resp.Code == minio.BucketAlreadyExists {
			return nil
		}
		return fmt.Errorf("objstore: MakeBucket: %w", err)
	}
	return nil
}

// BucketExists reports whether the configured bucket exists.
func (s *Store) BucketExists(ctx context.Context) (bool, error) {
	return s.client.BucketExists(ctx, s.bucket)
}

// Put uploads an object with EmailBridge metadata.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, meta ObjectMeta) error {
	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: ct,
		UserMetadata: map[string]string{
			metaOrg:     meta.Organization,
			metaAccount: meta.Account,
		},
	})
	if err != nil {
		return fmt.Errorf("objstore: Put: %w", err)
	}
	return nil
}

// Get downloads an object. The caller must Close the reader.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, mapErr(err)
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, ObjectInfo{}, mapErr(err)
	}
	return obj, fromMinio(st), nil
}

// Stat returns object metadata without downloading the body.
func (s *Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	st, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, mapErr(err)
	}
	return fromMinio(st), nil
}

// Delete removes one object.
func (s *Store) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// DeletePrefix removes every object under prefix (batch, for erasure S11 §5).
func (s *Store) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	var keys []minio.ObjectInfo
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return 0, mapErr(o.Err)
		}
		keys = append(keys, o)
	}
	if len(keys) == 0 {
		return 0, nil
	}
	ch := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		ch <- k
	}
	close(ch)
	n := 0
	for res := range s.client.RemoveObjectsWithResult(ctx, s.bucket, ch, minio.RemoveObjectsOptions{}) {
		if res.Err != nil {
			return n, fmt.Errorf("objstore: DeletePrefix: %w", res.Err)
		}
		n++
	}
	return n, nil
}

// List calls fn for every object under prefix. fn may return an error to stop early.
func (s *Store) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return mapErr(o.Err)
		}
		if err := fn(fromMinio(o)); err != nil {
			return err
		}
	}
	return nil
}

// PresignGet returns a signed GET URL. ttl must be positive and at most 7 days.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > maxPresign {
		return "", fmt.Errorf("objstore: presign ttl must be in (0, %s]", maxPresign)
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("objstore: PresignGet: %w", err)
	}
	return u.String(), nil
}

func fromMinio(o minio.ObjectInfo) ObjectInfo {
	info := ObjectInfo{
		Key:          o.Key,
		Size:         o.Size,
		ContentType:  o.ContentType,
		ETag:         o.ETag,
		LastModified: o.LastModified,
	}
	meta := o.UserMetadata
	if meta == nil {
		meta = map[string]string{}
	}
	info.Organization = metaValue(meta, metaOrg)
	info.Account = metaValue(meta, metaAccount)
	return info
}

func metaValue(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) || strings.EqualFold(k, "x-amz-meta-"+key) {
			return v
		}
	}
	return ""
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == minio.NoSuchKey || resp.Code == "NotFound" || resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return err
}
