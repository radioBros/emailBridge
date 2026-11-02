package egress

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDialViaSOCKS5Proxy(t *testing.T) {
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
			_, _ = c.Write([]byte("hi"))
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
	conn, err := d.DialContextOpts(context.Background(), "tcp", target.Addr().String(), Mail, DialOptions{
		ProxyURL: "socks5://" + proxyLn.Addr().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	n, err := conn.Read(buf)
	_ = conn.Close()
	if err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("via socks5: n=%d err=%v body=%q", n, err, buf[:n])
	}

	if _, err := d.DialContextOpts(context.Background(), "tcp", target.Addr().String(), Mail, DialOptions{ProxyURL: ":"}); err == nil {
		t.Fatal("bad proxy url")
	}
	if _, err := d.DialContextOpts(context.Background(), "tcp", target.Addr().String(), Mail, DialOptions{ProxyURL: "ftp://127.0.0.1:1"}); err == nil {
		t.Fatal("bad scheme")
	}
	_ = hostOfAddr("no-port")
	_ = hostOfAddr("127.0.0.1:25")
	f := &fixedDialer{}
	if _, err := f.Dial("tcp", "x"); err == nil {
		t.Fatal("fixed dialer empty")
	}

	proxyAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") == "" {
			http.Error(w, "auth", http.StatusProxyAuthRequired)
			return
		}
		dest, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = dest.Close()
			return
		}
		client, _, err := hj.Hijack()
		if err != nil {
			_ = dest.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		errc := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(dest, client); errc <- struct{}{} }()
		go func() { _, _ = io.Copy(client, dest); errc <- struct{}{} }()
		<-errc
		_ = client.Close()
		_ = dest.Close()
	}))
	defer proxyAuth.Close()
	hostPort := strings.TrimPrefix(proxyAuth.URL, "http://")
	conn2, err := d.DialContextOpts(context.Background(), "tcp", target.Addr().String(), Mail, DialOptions{
		ProxyURL: "http://user:pass@" + hostPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn2.Read(make([]byte, 2))
	_ = conn2.Close()

	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	rc := &replayConn{Conn: a, r: strings.NewReader("ab")}
	n, _ = rc.Read(make([]byte, 2))
	if n != 2 {
		t.Fatalf("replay read %d", n)
	}
	_, _ = parseLocalAddr("127.0.0.1:0")
}

func socks5Serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := make([]byte, 512)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return
	}
	_, _ = c.Write([]byte{0x05, 0x00})
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 0x01:
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03:
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(c, buf[:l]); err != nil {
			return
		}
		host = string(buf[:l])
	default:
		return
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(buf[:2]))
	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, itoaPort(port)), 3*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = target.Close() }()
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, c); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(c, target); errc <- struct{}{} }()
	<-errc
}

func itoaPort(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
