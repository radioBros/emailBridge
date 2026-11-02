package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestPolicyCheck(t *testing.T) {
	strict := Policy{BlockLinkLocal: true, BlockPrivate: true}
	open := Policy{}
	tests := []struct {
		addr    string
		p       Policy
		purpose Purpose
		ok      bool
	}{
		{"93.184.216.34", strict, HTTP, true},
		{"169.254.169.254", open, HTTP, false},
		{"169.254.169.254", open, Mail, false},
		{"::ffff:169.254.169.254", open, HTTP, false},
		{"fd00:ec2::254", open, HTTP, false},
		{"169.254.10.10", strict, HTTP, false},
		{"169.254.10.10", open, HTTP, true},
		{"169.254.10.10", open, Mail, false},
		{"fe80::1", strict, HTTP, false},
		{"10.0.0.5", strict, HTTP, false},
		{"10.0.0.5", open, HTTP, true},
		{"10.0.0.5", strict, Mail, true},
		{"127.0.0.1", strict, HTTP, false},
		{"100.64.1.1", strict, HTTP, false},
		{"fc00::1", strict, HTTP, false},
		{"0.0.0.0", open, HTTP, false},
		{"224.0.0.1", open, Mail, false},
	}
	for _, tt := range tests {
		ok, reason := tt.p.Check(ip(tt.addr), tt.purpose)
		if ok != tt.ok {
			t.Errorf("Check(%s, %+v, %d) = %v (%s), want %v", tt.addr, tt.p, tt.purpose, ok, reason, tt.ok)
		}
	}
	allow := Policy{BlockPrivate: true, Allow: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}}
	if ok, _ := allow.Check(ip("10.1.2.3"), HTTP); !ok {
		t.Error("allow-list must override the private block")
	}
	if ok, _ := (Policy{Allow: []netip.Prefix{netip.MustParsePrefix("169.254.169.254/32")}}).Check(ip("169.254.169.254"), HTTP); !ok {
		t.Error("an explicit allow entry overrides even metadata (operator's choice)")
	}
}

func TestParseAllow(t *testing.T) {
	p, h, err := ParseAllow([]string{"10.0.0.0/8", "192.168.1.5", " Hooks.Internal ", "", "fd00::/8"})
	if err != nil || len(p) != 3 || len(h) != 1 || h[0] != "hooks.internal" || p[1].Bits() != 32 {
		t.Fatalf("ParseAllow = %v %v %v", p, h, err)
	}
	if _, _, err := ParseAllow([]string{"bad entry/x"}); err == nil {
		t.Fatal("invalid entry accepted")
	}
}

func TestDialerBlocksRebindingAndMetadata(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	var blocked []string
	d := NewDialer(Policy{BlockLinkLocal: true, BlockPrivate: true})
	d.Resolver = fakeResolver{
		"evil.example":   {ip("127.0.0.1")},                 // rebinding to loopback
		"mixed.example":  {ip("10.0.0.1"), ip("127.0.0.1")}, // all blocked
		"public.example": {ip("127.0.0.1")},                 // allowed by host below
	}
	d.Blocked = func(host string, _ netip.Addr, reason string) { blocked = append(blocked, host+":"+reason) }
	ctx := context.Background()
	if _, err := d.DialContext(ctx, "tcp", "evil.example:"+port, HTTP); !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebinding must be blocked: %v", err)
	}
	if _, err := d.DialContext(ctx, "tcp", "mixed.example:"+port, HTTP); !errors.Is(err, ErrBlocked) {
		t.Fatalf("mixed: %v", err)
	}
	if _, err := d.DialContext(ctx, "tcp", "metadata.google.internal:80", HTTP); !errors.Is(err, ErrBlocked) {
		t.Fatalf("metadata host: %v", err)
	}
	if _, err := d.DialContext(ctx, "tcp", "169.254.169.254:80", Mail); !errors.Is(err, ErrBlocked) {
		t.Fatalf("metadata ip for mail: %v", err)
	}
	if len(blocked) < 4 {
		t.Fatalf("Blocked callback calls = %v", blocked)
	}
	// mail purpose may reach private/loopback servers
	c, err := d.DialContext(ctx, "tcp", "evil.example:"+port, Mail)
	if err != nil {
		t.Fatalf("mail to loopback: %v", err)
	}
	_ = c.Close()
	// allow-host overrides
	_, hosts, _ := ParseAllow([]string{"public.example"})
	pol := d.Policy()
	pol.AllowHosts = hosts
	d.SetPolicy(pol)
	c, err = d.DialContext(ctx, "tcp", "public.example:"+port, HTTP)
	if err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	_ = c.Close()
	if _, err := d.DialContext(ctx, "tcp", "unknown.example:80", HTTP); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("resolve failure: %v", err)
	}
	if _, err := d.DialContext(ctx, "tcp", "no-port", HTTP); err == nil {
		t.Fatal("bad address")
	}
	// literal IP that is allowed but nothing listens → dial error, not ErrBlocked
	d.SetPolicy(Policy{})
	if _, err := d.DialContext(ctx, "tcp", "127.0.0.1:1", HTTP); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("dial failure: %v", err)
	}
}

func TestHTTPClientNoRedirectsAndPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer srv.Close()
	d := NewDialer(Policy{})
	resp, err := d.HTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect must not be followed, got %d", resp.StatusCode)
	}
	d.SetPolicy(Policy{BlockPrivate: true})
	resp2, err := d.HTTPClient(time.Second).Get(srv.URL)
	if err == nil {
		_ = resp2.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback webhook must be blocked: %v", err)
	}
}

func TestCheckURLHost(t *testing.T) {
	d := NewDialer(Policy{BlockPrivate: true, BlockLinkLocal: true})
	d.Resolver = fakeResolver{"ok.example": {ip("93.184.216.34")}, "bad.example": {ip("10.0.0.1")}}
	ctx := context.Background()
	if err := d.CheckURLHost(ctx, "ok.example", HTTP); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"bad.example", "metadata.google.internal", "127.0.0.1"} {
		if err := d.CheckURLHost(ctx, h, HTTP); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", h, err)
		}
	}
	if err := d.CheckURLHost(ctx, "missing.example", HTTP); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("resolve failure: %v", err)
	}
	_, hosts, _ := ParseAllow([]string{"bad.example"})
	d.SetPolicy(Policy{BlockPrivate: true, AllowHosts: hosts})
	if err := d.CheckURLHost(ctx, "bad.example", HTTP); err != nil {
		t.Fatalf("allowed host: %v", err)
	}
}

func TestDialViaHTTPConnectProxy(t *testing.T) {
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
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		dest, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
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
	defer proxy.Close()
	d := NewDialer(Policy{})
	conn, err := d.DialContextOpts(context.Background(), "tcp", target.Addr().String(), Mail, DialOptions{
		ProxyURL: proxy.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	n, err := conn.Read(buf)
	_ = conn.Close()
	if err != nil || n != 2 || string(buf) != "ok" {
		t.Fatalf("read via proxy: n=%d err=%v body=%q", n, err, buf)
	}
	if _, err := parseLocalAddr("not-an-ip"); err == nil {
		t.Fatal("bad localAddress")
	}
	if _, err := parseLocalAddr("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
}
