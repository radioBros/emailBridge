//go:build integration

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/containers"
)

// TestServeRunsUntilCancelled boots a real node (Mongo replica set, embedded NATS, S3), waits for
// /readyz and stops it cleanly.
func TestServeRunsUntilCancelled(t *testing.T) {
	minimalEnv(t)
	t.Setenv("EB_MONGO_URI", containers.MongoShared(t))
	t.Setenv("EB_MONGO_DATABASE", containers.Database(t))
	ep, ak, sk := containers.S3(t)
	t.Setenv("EB_S3_ENDPOINT", "http://"+ep)
	t.Setenv("EB_S3_ACCESS_KEY", ak)
	t.Setenv("EB_S3_SECRET_KEY", sk)
	addr := freeAddr(t)
	t.Setenv("EB_HTTP_ADDR", addr)
	t.Setenv("EB_METRICS_ADDR", freeAddr(t))
	t.Setenv("EB_ROLES", "all")
	t.Setenv("EB_NODE_ID", "")
	t.Setenv("EB_SHUTDOWN_GRACE", "1s")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, nil, io.Discard) }()
	stopped := false
	t.Cleanup(func() { // stop the node before TempDir cleanup, even on a failed assertion
		cancel()
		if !stopped {
			select {
			case err := <-done:
				t.Logf("serve stopped: %v", err)
			case <-time.After(30 * time.Second):
			}
		}
	})
	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/readyz") //nolint:noctx // test poll
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("serve stopped early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("node never became ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}
