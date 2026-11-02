package egress

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestDialMailLocalAndProxyPassthrough(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(io.Discard, c)
	}()

	d := NewDialer(Policy{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.DialMail(ctx, "tcp", ln.Addr().String(), MailNetwork{LocalAddr: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	fn := d.MailDialFunc(MailNetwork{})
	conn2, err := fn(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn2.Close()
}
