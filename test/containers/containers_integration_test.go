//go:build integration

package containers_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/containers"
)

func TestMongoSmoke(t *testing.T) {
	uri := containers.Mongo(t)
	if uri == "" || !strings.Contains(uri, "mongodb://") {
		t.Fatalf("unexpected uri %q", uri)
	}
	db := containers.Database(t)
	if db == "" {
		t.Fatal("empty database name")
	}
}

func TestMongoSharedSmoke(t *testing.T) {
	a := containers.MongoShared(t)
	b := containers.MongoShared(t)
	if a != b {
		t.Fatalf("MongoShared not stable: %q vs %q", a, b)
	}
}

func TestNATSSmoke(t *testing.T) {
	url := containers.NATS(t)
	if !strings.HasPrefix(url, "nats://") {
		t.Fatalf("unexpected url %q", url)
	}
}

func TestS3Smoke(t *testing.T) {
	ep, ak, sk := containers.S3(t)
	if ep == "" || ak == "" || sk == "" {
		t.Fatalf("empty s3 creds: %q %q %q", ep, ak, sk)
	}
}

func TestDovecotSmoke(t *testing.T) {
	addr, pass := containers.Dovecot(t)
	if addr == "" || pass == "" {
		t.Fatalf("empty dovecot: %q %q", addr, pass)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial imap: %v", err)
	}
	_ = conn.Close()
}
