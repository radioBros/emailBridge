package objstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/objstore"
)

func TestArchiveKeyRejectsBad(t *testing.T) {
	_, err := objstore.ArchiveKey("org", "acc/x", "msg", time.Now())
	if !errors.Is(err, objstore.ErrBadKey) {
		t.Fatalf("%v", err)
	}
}

func TestErrNotConnectedMessage(t *testing.T) {
	if objstore.ErrNotConnected.Error() == "" {
		t.Fatal("empty")
	}
}

func TestPresignGetOK(t *testing.T) {
	s, err := objstore.Open(objstore.Options{
		Endpoint: "http://127.0.0.1:9000", Bucket: "bucket", AccessKey: "a", SecretKey: "s", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.PresignGet(context.Background(), "k", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u == "" {
		t.Fatal("empty url")
	}
}
