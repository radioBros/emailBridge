package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
)

func TestDialMailViaSOCKS5(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("ok"))
			_ = c.Close()
		}
	}()
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxyLn.Close() }()
	go func() {
		for {
			c, err := proxyLn.Accept()
			if err != nil {
				return
			}
			go socks5Serve(c)
		}
	}()

	d := NewDialer(Policy{})
	conn, err := d.DialMail(context.Background(), "tcp", target.Addr().String(), MailNetwork{
		Proxy: "socks5://" + proxyLn.Addr().String(), LocalAddr: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	n, err := conn.Read(buf)
	_ = conn.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ok" {
		t.Fatalf("body %q", buf[:n])
	}
}
