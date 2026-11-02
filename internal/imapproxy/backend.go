package imapproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// dialBackend opens and authenticates a connection to the real IMAP server.
func dialBackend(ctx context.Context, t *IMAPTarget, dial DialFunc) (net.Conn, error) {
	if dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second}
		dial = d.DialContext
	}
	raw, err := dial(ctx, "tcp", t.Addr)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(t.Addr)
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: t.InsecureSkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // G402: account ignoreCertErrors
	var conn net.Conn
	switch t.TLS {
	case imapc.TLSImplicit:
		tc := tls.Client(raw, tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		conn = tc
	case imapc.TLSStartTLS:
		conn = raw
	default:
		conn = raw
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	br := newLineReader(conn)
	if _, err := br.readLine(); err != nil { // greeting
		_ = conn.Close()
		return nil, err
	}
	if t.TLS == imapc.TLSStartTLS {
		if err := writeLine(conn, "A001 STARTTLS"); err != nil {
			_ = conn.Close()
			return nil, err
		}
		line, err := br.readLine()
		if err != nil || !strings.HasPrefix(line, "A001 OK") {
			_ = conn.Close()
			return nil, fmt.Errorf("imapproxy: STARTTLS failed: %s", line)
		}
		tc := tls.Client(conn, tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tc
		br = newLineReader(conn)
	}
	if t.AccessToken != "" {
		payload := "user=" + t.Username + "\x01auth=Bearer " + t.AccessToken + "\x01\x01"
		b64 := base64.StdEncoding.EncodeToString([]byte(payload))
		if err := writeLine(conn, "A002 AUTHENTICATE XOAUTH2 "+b64); err != nil {
			_ = conn.Close()
			return nil, err
		}
	} else {
		user := quoteIMAP(t.Username)
		pass := quoteIMAP(t.Password)
		if err := writeLine(conn, "A002 LOGIN "+user+" "+pass); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	line, err := br.readLine()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Skip untagged lines until tagged A002.
	for !strings.HasPrefix(line, "A002 ") {
		line, err = br.readLine()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if !strings.HasPrefix(line, "A002 OK") {
		_ = conn.Close()
		return nil, fmt.Errorf("imapproxy: backend auth failed")
	}
	_ = conn.SetDeadline(time.Time{})
	// Prefetch any leftover buffered bytes into a multi-reader wrapper.
	if pend := br.buffered(); len(pend) > 0 {
		return &bufConn{Conn: conn, r: io.MultiReader(strings.NewReader(string(pend)), conn)}, nil
	}
	return conn, nil
}

func quoteIMAP(s string) string {
	if s == "" {
		return `""`
	}
	need := false
	for _, c := range s {
		if c <= ' ' || c == '"' || c == '\\' || c == '{' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		if c == '\\' || c == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	b.WriteByte('"')
	return b.String()
}

func writeLine(w io.Writer, s string) error {
	_, err := io.WriteString(w, s+"\r\n")
	return err
}

type bufConn struct {
	net.Conn
	r io.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// splice copies both directions until either side closes.
func splice(client, backend net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(backend, client)
		_ = backend.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, backend)
		_ = client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}
