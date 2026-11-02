package imapproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"
)

// connHandler speaks IMAP until AUTH, then splices to the backend.
type connHandler struct {
	auth     *Authenticator
	source   IMAPSource
	dial     DialFunc
	budget   *budget
	log      *slog.Logger
	gate     FeatureGate
	tlsConf  *tls.Config
	insecure bool // allow AUTH without TLS
}

func (h *connHandler) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	remote := ""
	if a := conn.RemoteAddr(); a != nil {
		remote = a.String()
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	br := newLineReader(conn)
	if err := writeLine(conn, "* OK EmailBridge IMAP proxy ready"); err != nil {
		return
	}
	tlsOn := isTLS(conn)
	for {
		line, err := br.readLine()
		if err != nil {
			return
		}
		tag, cmd, args := splitIMAP(line)
		if tag == "" {
			_ = writeLine(conn, "* BAD Missing tag")
			continue
		}
		switch strings.ToUpper(cmd) {
		case "CAPABILITY":
			caps := "* CAPABILITY IMAP4rev1 AUTH=PLAIN AUTH=LOGIN"
			if h.tlsConf != nil && !tlsOn {
				caps += " STARTTLS"
			}
			_ = writeLine(conn, caps)
			_ = writeLine(conn, tag+" OK CAPABILITY completed")
		case "NOOP":
			_ = writeLine(conn, tag+" OK NOOP completed")
		case "LOGOUT":
			_ = writeLine(conn, "* BYE Logging out")
			_ = writeLine(conn, tag+" OK LOGOUT completed")
			return
		case "STARTTLS":
			if h.tlsConf == nil || tlsOn {
				_ = writeLine(conn, tag+" BAD STARTTLS not available")
				continue
			}
			_ = writeLine(conn, tag+" OK Begin TLS negotiation")
			tc := tls.Server(conn, h.tlsConf)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn = tc
			br = newLineReader(conn)
			tlsOn = true
		case "LOGIN":
			if !tlsOn && !h.insecure {
				_ = writeLine(conn, tag+" NO [PRIVACYREQUIRED] TLS required")
				continue
			}
			user, pass, ok := parseLoginArgs(args)
			if !ok {
				_ = writeLine(conn, tag+" BAD Invalid arguments")
				continue
			}
			h.doAuth(conn, br, tag, user, pass, remote)
			return
		case "AUTHENTICATE":
			if !tlsOn && !h.insecure {
				_ = writeLine(conn, tag+" NO [PRIVACYREQUIRED] TLS required")
				continue
			}
			mech := strings.ToUpper(strings.TrimSpace(args))
			if mech != "PLAIN" {
				_ = writeLine(conn, tag+" NO Unsupported mechanism")
				continue
			}
			_ = writeLine(conn, "+")
			cont, err := br.readLine()
			if err != nil {
				return
			}
			if cont == "*" {
				_ = writeLine(conn, tag+" BAD Authentication cancelled")
				continue
			}
			user, pass, ok := decodePlain(cont)
			if !ok {
				_ = writeLine(conn, tag+" NO Authentication failed")
				continue
			}
			h.doAuth(conn, br, tag, user, pass, remote)
			return
		default:
			_ = writeLine(conn, tag+" BAD Command not available before authentication")
		}
	}
}

func (h *connHandler) doAuth(client net.Conn, br *lineReader, tag, user, pass, remote string) {
	if h.gate != nil && !h.gate.IMAPProxyEnabled(context.Background()) {
		_ = writeLine(client, tag+" NO [UNAVAILABLE] Proxy disabled")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acc, err := h.auth.Authenticate(ctx, user, pass, remote)
	if err != nil {
		_ = writeLine(client, tag+" NO [AUTHENTICATIONFAILED] Authentication failed")
		return
	}
	if !h.budget.acquire(acc.ID) {
		_ = writeLine(client, tag+" NO [UNAVAILABLE] Connection limit reached")
		return
	}
	held := true
	defer func() {
		if held {
			h.budget.release(acc.ID)
		}
	}()

	target, err := h.source.LoadIMAP(ctx, acc.ID)
	if err != nil || target == nil {
		_ = writeLine(client, tag+" NO [UNAVAILABLE] Backend unavailable")
		return
	}
	backend, err := dialBackend(ctx, target, h.dial)
	if err != nil {
		if h.log != nil {
			h.log.Info("imapproxy backend dial failed", "accountId", acc.ID, "err", err.Error())
		}
		_ = writeLine(client, tag+" NO [UNAVAILABLE] Backend unavailable")
		return
	}

	// Any bytes already buffered from the client side must be forwarded after OK.
	pending := br.buffered()
	_ = writeLine(client, tag+" OK LOGIN completed")
	_ = client.SetDeadline(time.Time{})
	if len(pending) > 0 {
		client = &bufConn{Conn: client, r: io.MultiReader(strings.NewReader(string(pending)), client)}
	}
	held = false // splice owns the slot until it returns
	defer h.budget.release(acc.ID)
	defer func() { _ = backend.Close() }()
	splice(client, backend)
}

func isTLS(c net.Conn) bool {
	_, ok := c.(*tls.Conn)
	return ok
}

func splitIMAP(line string) (tag, cmd, args string) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) == 0 {
		return "", "", ""
	}
	tag = parts[0]
	if len(parts) == 1 {
		return tag, "", ""
	}
	cmd = parts[1]
	if len(parts) == 3 {
		args = parts[2]
	}
	return tag, cmd, args
}

func parseLoginArgs(args string) (user, pass string, ok bool) {
	u, rest, ok1 := readAstring(args)
	if !ok1 {
		return "", "", false
	}
	p, _, ok2 := readAstring(strings.TrimSpace(rest))
	if !ok2 {
		return "", "", false
	}
	return u, p, true
}

func readAstring(s string) (val, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' {
		var b strings.Builder
		i := 1
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if s[i] == '"' {
				return b.String(), s[i+1:], true
			}
			b.WriteByte(s[i])
			i++
		}
		return "", "", false
	}
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, "", true
	}
	return s[:i], s[i:], true
}

func decodePlain(b64 string) (user, pass string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}
