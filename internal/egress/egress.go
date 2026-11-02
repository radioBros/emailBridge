// Package egress is the one place where EmailBridge opens connections to destinations chosen by users:
// webhook endpoints, sinks, mail servers, OIDC discovery, integrations (S10 §8). It resolves the host,
// checks every IP it is about to dial against the policy, and dials that IP — so DNS rebinding cannot
// slip a private address past the check.
package egress

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// ErrBlocked is returned (wrapped) when the policy forbids the destination.
var ErrBlocked = errors.New("egress: destination blocked by policy")

// Purpose selects which rules apply.
type Purpose int

const (
	// HTTP is any HTTP-type egress (webhooks, sinks, OIDC, integrations, hooks).
	HTTP Purpose = iota
	// Mail is IMAP/SMTP to mail servers, which are often on private networks: only the always-blocked
	// list applies (link-local, metadata, unspecified, multicast).
	Mail
)

// Policy is the runtime egress policy (EB_EGRESS_*, S9 §2.11).
type Policy struct {
	BlockLinkLocal bool // HTTP only; always on for metadata addresses regardless
	BlockPrivate   bool // HTTP only
	Allow          []netip.Prefix
	AllowHosts     []string // exact host names (lowercase) allowed regardless of their IPs
}

var (
	metadataAddrs = []netip.Addr{netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("fd00:ec2::254"), netip.MustParseAddr("100.100.100.200")}
	metadataHosts = map[string]bool{"metadata.google.internal": true, "metadata": true, "instance-data": true}
	cgnat         = netip.MustParsePrefix("100.64.0.0/10")
)

// ParseAllow turns EB_EGRESS_ALLOW items into prefixes (bare IPs become /32 or /128) and host names.
func ParseAllow(items []string) (prefixes []netip.Prefix, hosts []string, err error) {
	for _, it := range items {
		it = strings.TrimSpace(strings.ToLower(it))
		if it == "" {
			continue
		}
		if p, perr := netip.ParsePrefix(it); perr == nil {
			prefixes = append(prefixes, p.Masked())
			continue
		}
		if a, aerr := netip.ParseAddr(it); aerr == nil {
			prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		if strings.ContainsAny(it, "/ ") {
			return nil, nil, fmt.Errorf("egress: invalid allow entry %q", it)
		}
		hosts = append(hosts, it)
	}
	return prefixes, hosts, nil
}

// Check decides whether addr may be dialed for purpose. It returns a reason when blocked.
func (p Policy) Check(addr netip.Addr, purpose Purpose) (bool, string) {
	addr = addr.Unmap()
	for _, a := range p.Allow {
		if a.Contains(addr) {
			return true, ""
		}
	}
	for _, m := range metadataAddrs {
		if addr == m {
			return false, "cloud metadata address"
		}
	}
	switch {
	case addr.IsUnspecified():
		return false, "unspecified address"
	case addr.IsMulticast():
		return false, "multicast address"
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		if purpose == Mail || p.BlockLinkLocal {
			return false, "link-local address"
		}
	}
	if purpose == Mail {
		return true, ""
	}
	if p.BlockPrivate && (addr.IsPrivate() || addr.IsLoopback() || cgnat.Contains(addr)) {
		return false, "private address"
	}
	return true, ""
}

// Resolver looks up host addresses (net.DefaultResolver in production; fakes in tests).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer dials TCP connections under a policy. The policy can be swapped at runtime (settings changes).
type Dialer struct {
	policy   atomic.Pointer[Policy]
	Resolver Resolver
	Timeout  time.Duration
	// Blocked is called for every refused destination (security log, metrics).
	Blocked func(host string, addr netip.Addr, reason string)
	// publicOf, when set, makes this a PublicOnly view of that dialer.
	publicOf *Dialer
}

// PublicOnly returns a dialer that follows d's policy (including runtime changes and EB_EGRESS_ALLOW) but
// always blocks private and link-local destinations, whatever EB_EGRESS_BLOCK_PRIVATE says. It is for
// lookups that only ever need public hosts and whose target an anonymous caller can influence — autoconfig
// discovery from the public connect page derives URLs from any email domain.
func (d *Dialer) PublicOnly() *Dialer {
	return &Dialer{Resolver: d.Resolver, Timeout: d.Timeout, Blocked: d.Blocked, publicOf: d}
}

// load returns the effective policy.
func (d *Dialer) load() *Policy {
	if d.publicOf != nil {
		p := *d.publicOf.load()
		p.BlockPrivate, p.BlockLinkLocal = true, true
		return &p
	}
	return d.policy.Load()
}

// NewDialer returns a dialer with the given policy and the system resolver.
func NewDialer(p Policy) *Dialer {
	d := &Dialer{Resolver: net.DefaultResolver, Timeout: 30 * time.Second}
	d.SetPolicy(p)
	return d
}

// SetPolicy replaces the policy.
func (d *Dialer) SetPolicy(p Policy) { d.policy.Store(&p) }

// Policy returns the current policy.
func (d *Dialer) Policy() Policy { return *d.load() }

func (d *Dialer) hostAllowed(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range d.load().AllowHosts {
		if a == h {
			return true
		}
	}
	return false
}

// DialOptions customise a single dial (account/gateway network settings, P5).
type DialOptions struct {
	// LocalAddr is an optional bind address (IP literal or host: ignored port).
	LocalAddr string
	// ProxyURL is socks5://, socks5h://, http:// or https:// (HTTP CONNECT). Empty = direct.
	ProxyURL string
}

// DialContext resolves address, refuses blocked IPs and dials the first allowed one.
func (d *Dialer) DialContext(ctx context.Context, network, address string, purpose Purpose) (net.Conn, error) {
	return d.DialContextOpts(ctx, network, address, purpose, DialOptions{})
}

// DialContextOpts is DialContext with optional local bind and proxy (P5).
func (d *Dialer) DialContextOpts(ctx context.Context, network, address string, purpose Purpose, opts DialOptions) (net.Conn, error) {
	if opts.ProxyURL != "" {
		return d.dialViaProxy(ctx, network, address, purpose, opts)
	}
	return d.dialDirect(ctx, network, address, purpose, opts)
}

func (d *Dialer) dialDirect(ctx context.Context, network, address string, purpose Purpose, opts DialOptions) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	lh := strings.ToLower(strings.TrimSuffix(host, "."))
	if metadataHosts[lh] && !d.hostAllowed(lh) {
		d.report(host, netip.Addr{}, "cloud metadata host")
		return nil, fmt.Errorf("%w: %s (cloud metadata host)", ErrBlocked, host)
	}
	var addrs []netip.Addr
	if a, perr := netip.ParseAddr(host); perr == nil {
		addrs = []netip.Addr{a}
	} else {
		addrs, err = d.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("egress: resolve %s: %w", host, err)
		}
	}
	pol := d.load()
	allowedHost := d.hostAllowed(lh)
	var lastReason string
	var dialErr error
	nd := &net.Dialer{Timeout: d.Timeout, KeepAliveConfig: net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 3}}
	if la, lerr := parseLocalAddr(opts.LocalAddr); lerr != nil {
		return nil, lerr
	} else if la != nil {
		nd.LocalAddr = la
	}
	for _, a := range addrs {
		if !allowedHost {
			if ok, reason := pol.Check(a, purpose); !ok {
				lastReason = reason
				d.report(host, a, reason)
				continue
			}
		}
		conn, err := nd.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr != nil {
		return nil, dialErr
	}
	if lastReason == "" {
		lastReason = "no addresses"
	}
	return nil, fmt.Errorf("%w: %s (%s)", ErrBlocked, host, lastReason)
}

func parseLocalAddr(s string) (net.Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("egress: invalid localAddress %q", s)
	}
	return &net.TCPAddr{IP: ip}, nil
}

func (d *Dialer) dialViaProxy(ctx context.Context, network, address string, purpose Purpose, opts DialOptions) (net.Conn, error) {
	u, err := url.Parse(opts.ProxyURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("egress: invalid proxy URL")
	}
	scheme := strings.ToLower(u.Scheme)
	proxyAddr := u.Host
	if _, _, err := net.SplitHostPort(proxyAddr); err != nil {
		switch scheme {
		case "http", "https":
			proxyAddr = net.JoinHostPort(proxyAddr, "80")
		default:
			proxyAddr = net.JoinHostPort(proxyAddr, "1080")
		}
	}
	// Proxy itself is an HTTP-class destination for SSRF checks.
	proxyConn, err := d.dialDirect(ctx, "tcp", proxyAddr, HTTP, DialOptions{LocalAddr: opts.LocalAddr})
	if err != nil {
		return nil, err
	}
	// Still check the final destination IPs (Mail or HTTP) before asking the proxy to connect.
	if err := d.CheckURLHost(ctx, hostOfAddr(address), purpose); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	switch scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pass}
		}
		s5, err := proxy.SOCKS5("tcp", proxyAddr, auth, &fixedDialer{c: proxyConn})
		if err != nil {
			_ = proxyConn.Close()
			return nil, err
		}
		type contextDialer interface {
			DialContext(context.Context, string, string) (net.Conn, error)
		}
		if cd, ok := s5.(contextDialer); ok {
			return cd.DialContext(ctx, network, address)
		}
		return s5.Dial(network, address)
	case "http", "https":
		return httpConnect(ctx, proxyConn, address, u.User)
	default:
		_ = proxyConn.Close()
		return nil, fmt.Errorf("egress: unsupported proxy scheme %q", scheme)
	}
}

func hostOfAddr(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

// fixedDialer returns an already-open connection once (for SOCKS5 over a policy-checked dial).
type fixedDialer struct{ c net.Conn }

func (f *fixedDialer) Dial(_, _ string) (net.Conn, error) {
	if f.c == nil {
		return nil, errors.New("egress: proxy connection already used")
	}
	c := f.c
	f.c = nil
	return c, nil
}

func httpConnect(ctx context.Context, proxyConn net.Conn, address string, user *url.Userinfo) (net.Conn, error) {
	var b strings.Builder
	b.WriteString("CONNECT ")
	b.WriteString(address)
	b.WriteString(" HTTP/1.1\r\nHost: ")
	b.WriteString(address)
	b.WriteString("\r\n")
	if user != nil {
		pass, _ := user.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + pass))
		b.WriteString("Proxy-Authorization: Basic ")
		b.WriteString(token)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	if deadline, ok := ctx.Deadline(); ok {
		_ = proxyConn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(proxyConn, b.String()); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	br := bufio.NewReader(proxyConn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = proxyConn.Close()
		return nil, fmt.Errorf("egress: proxy CONNECT %s", resp.Status)
	}
	_ = proxyConn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &replayConn{Conn: proxyConn, r: io.MultiReader(br, proxyConn)}, nil
	}
	return proxyConn, nil
}

type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func (d *Dialer) report(host string, a netip.Addr, reason string) {
	if d.Blocked != nil {
		d.Blocked(host, a, reason)
	}
}

// HTTPClient returns a client for HTTP egress: policy-checked dials, no environment proxy, no redirects
// (a 3xx is returned as the response, S7 §2.1), TLS verification on.
func (d *Dialer) HTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr, HTTP)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// CheckURLHost validates a URL host at configuration time (webhook create/update): it resolves the host
// and fails when every address is blocked. Delivery-time checks still happen on every dial.
func (d *Dialer) CheckURLHost(ctx context.Context, host string, purpose Purpose) error {
	lh := strings.ToLower(strings.TrimSuffix(host, "."))
	if d.hostAllowed(lh) {
		return nil
	}
	if metadataHosts[lh] {
		return fmt.Errorf("%w: %s (cloud metadata host)", ErrBlocked, host)
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		var err error
		addrs, err = d.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return fmt.Errorf("egress: resolve %s: %w", host, err)
		}
	}
	pol := d.load()
	for _, a := range addrs {
		if ok, _ := pol.Check(a, purpose); ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrBlocked, host)
}
