package main

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/connect"
)

func TestConnectHostedConnectHook(t *testing.T) {
	m := &connect.Module{Env: "test", PublicURL: "https://eb.example"}
	h := connectHostedConnect(m)
	if h.Name() != "wire.connect" {
		t.Fatalf("name %s", h.Name())
	}
	if len(h.Roles()) == 0 {
		t.Fatal("roles")
	}
}
