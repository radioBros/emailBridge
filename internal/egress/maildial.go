package egress

import (
	"context"
	"net"
)

// MailNetwork is proxy + local bind for one mail dial (W57). Callers resolve IP-pool
// picks into LocalAddr before dialling.
type MailNetwork struct {
	Proxy     string
	LocalAddr string
}

// DialMail dials a mail destination with optional proxy and local bind through the
// SSRF-safe dialer (Purpose Mail).
func (d *Dialer) DialMail(ctx context.Context, network, address string, n MailNetwork) (net.Conn, error) {
	return d.DialContextOpts(ctx, network, address, Mail, DialOptions{ProxyURL: n.Proxy, LocalAddr: n.LocalAddr})
}

// MailDialFunc returns a dial closure for IMAP/SMTP clients.
func (d *Dialer) MailDialFunc(n MailNetwork) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return d.DialMail(ctx, network, addr, n)
	}
}
