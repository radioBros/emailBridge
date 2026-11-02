//go:build integration

package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/test/containers"
)

func testStore(t *testing.T) *objstore.Store {
	t.Helper()
	ep, ak, sk := containers.S3(t)
	s, err := objstore.Open(objstore.Options{
		Endpoint:  "http://" + ep,
		Region:    "us-east-1",
		AccessKey: ak,
		SecretKey: sk,
		Bucket:    "emailbridge-test",
		PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket twice: %v", err)
	}
	return s
}

func TestIntegrationPutGetStatDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	key, err := objstore.OutboundKey("org_1", "acc_1", "out_1")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("From: a@b\r\n\r\nhello")
	err = s.Put(ctx, key, bytes.NewReader(body), int64(len(body)), objstore.ObjectMeta{
		Organization: "org_1", Account: "acc_1", ContentType: "message/rfc822",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Stat(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Organization != "org_1" || info.Account != "acc_1" || info.Size != int64(len(body)) {
		t.Fatalf("%+v", info)
	}
	rc, got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(rc)
	if !bytes.Equal(data, body) || got.Organization != "org_1" {
		t.Fatalf("get mismatch: %q %+v", data, got)
	}

	url, err := s.PresignGet(ctx, key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presign status %d", resp.StatusCode)
	}
	pdata, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(pdata, body) {
		t.Fatalf("presign body %q", pdata)
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	_, err = s.Stat(ctx, key)
	if !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestIntegrationListAndDeletePrefix(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prefix, err := objstore.ExportPrefix("org_x", "exp_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		key := prefix + name
		data := []byte(name)
		if err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), objstore.ObjectMeta{
			Organization: "org_x", Account: "acc_x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var listed []string
	if err := s.List(ctx, prefix, func(info objstore.ObjectInfo) error {
		listed = append(listed, info.Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %v", listed)
	}
	n, err := s.DeletePrefix(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d", n)
	}
	listed = nil
	_ = s.List(ctx, prefix, func(info objstore.ObjectInfo) error {
		listed = append(listed, info.Key)
		return nil
	})
	if len(listed) != 0 {
		t.Fatalf("still listed %v", listed)
	}
}

func TestIntegrationModule(t *testing.T) {
	ep, ak, sk := containers.S3(t)
	m := &objstore.Module{Options: objstore.Options{
		Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk,
		Bucket: "emailbridge-mod", PathStyle: true,
	}}
	a := app.New(app.Options{NodeID: "n1", Modules: []app.Module{m}})
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Stop(ctx) }()
	if err := m.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := app.MustGet[*objstore.Store](a)
	if err != nil || s == nil {
		t.Fatalf("provide: %v", err)
	}
}
