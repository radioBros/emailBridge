package smtpc

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
)

func TestDSNAppliedWhenAdvertised(t *testing.T) {
	_, addr := start(t, plain, func(_ *srv, server *smtp.Server) {
		server.EnableDSN = true
	})
	sess, err := Open(context.Background(), opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if !sess.Supports("DSN") {
		t.Fatal("server should advertise DSN")
	}
	r := strings.NewReader(body)
	_, err = sess.Send(context.Background(), Envelope{
		From: "a@example.test", To: []string{"ok@example.test"}, Size: int64(len(body)),
		DSN: &DSN{Notify: []string{"success", "failure"}, Ret: "hdrs", EnvID: "e1"},
	}, io.NopCloser(r), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDSNOmittedWithoutCapability(t *testing.T) {
	_, addr := start(t, plain, nil)
	sess, err := Open(context.Background(), opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if sess.Supports("DSN") {
		t.Fatal("DSN must be off by default in the scripted server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = sess.Send(ctx, Envelope{
		From: "a@example.test", To: []string{"ok@example.test"}, Size: int64(len(body)),
		DSN: &DSN{Notify: []string{"failure"}, Ret: "full", EnvID: "x"},
	}, io.NopCloser(strings.NewReader(body)), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
}
