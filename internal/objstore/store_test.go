package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/objstore"
)

func TestOpenRequiresEndpointAndBucket(t *testing.T) {
	_, err := objstore.Open(objstore.Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = objstore.Open(objstore.Options{Endpoint: "http://localhost:9000"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenParsesEndpoint(t *testing.T) {
	s, err := objstore.Open(objstore.Options{
		Endpoint: "http://127.0.0.1:9000", Bucket: "b", PathStyle: true,
		AccessKey: "a", SecretKey: "s",
	})
	if err != nil || s == nil {
		t.Fatalf("%v", err)
	}
	s2, err := objstore.Open(objstore.Options{
		Endpoint: "127.0.0.1:9000", Bucket: "b",
		AccessKey: "a", SecretKey: "s",
	})
	if err != nil || s2 == nil {
		t.Fatalf("%v", err)
	}
	_, err = objstore.Open(objstore.Options{Endpoint: "://", Bucket: "b"})
	if err == nil {
		t.Fatal("expected bad endpoint error")
	}
}

func TestOpenEmptyCredsUsesChain(t *testing.T) {
	s, err := objstore.Open(objstore.Options{
		Endpoint: "https://s3.eu-south-1.amazonaws.com", Bucket: "b", Region: "eu-south-1",
	})
	if err != nil || s == nil {
		t.Fatalf("%v", err)
	}
}

func TestPresignTTL(t *testing.T) {
	s, err := objstore.Open(objstore.Options{
		Endpoint: "http://127.0.0.1:9000", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PresignGet(context.Background(), "k", 0)
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("%v", err)
	}
	_, err = s.PresignGet(context.Background(), "k", 8*24*time.Hour)
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("%v", err)
	}
}

func TestModuleReadyBeforeStart(t *testing.T) {
	m := &objstore.Module{}
	if err := m.Ready(context.Background()); !errors.Is(err, objstore.ErrNotConnected) {
		t.Fatalf("%v", err)
	}
	if m.Name() != "objstore" || m.Roles() != nil {
		t.Fatal("module meta")
	}
	_ = m.Stop(context.Background())
	_ = bytes.NewReader(nil)
}
