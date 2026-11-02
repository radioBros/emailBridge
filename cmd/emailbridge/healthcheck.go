package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// healthcheck calls /readyz on this node's own HTTP listener over loopback. It is the container
// healthcheck: the distroless image has no shell or curl. Only the port is taken from EB_HTTP_ADDR; the
// host is always 127.0.0.1, so the check can never be pointed elsewhere.
func healthcheck(_ []string) error {
	addr := os.Getenv("EB_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("EB_HTTP_ADDR %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("EB_HTTP_ADDR %q: invalid port", addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G704: loopback-only URL built from a validated port number
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: loopback-only URL built from a validated port number
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/readyz returned %d", resp.StatusCode)
	}
	return nil
}
