package imapproxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
)

type alwaysOn struct{}

func (alwaysOn) IMAPProxyEnabled(context.Context) bool { return true }

type memSource struct {
	t *IMAPTarget
}

func (m *memSource) LoadIMAP(context.Context, string) (*IMAPTarget, error) { return m.t, nil }

// fakeIMAPBackend speaks a minimal authenticated IMAP server for splice tests.
func fakeIMAPBackend(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				_, _ = io.WriteString(conn, "* OK fake ready\r\n")
				br := bufio.NewReader(conn)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					parts := strings.SplitN(line, " ", 3)
					if len(parts) < 2 {
						continue
					}
					tag, cmd := parts[0], strings.ToUpper(parts[1])
					switch cmd {
					case "LOGIN":
						_, _ = io.WriteString(conn, tag+" OK LOGIN completed\r\n")
					case "CAPABILITY":
						_, _ = io.WriteString(conn, "* CAPABILITY IMAP4rev1\r\n")
						_, _ = io.WriteString(conn, tag+" OK\r\n")
					case "LIST":
						_, _ = io.WriteString(conn, `* LIST (\HasNoChildren) "." INBOX`+"\r\n")
						_, _ = io.WriteString(conn, tag+" OK LIST completed\r\n")
					case "LOGOUT":
						_, _ = io.WriteString(conn, "* BYE\r\n"+tag+" OK\r\n")
						return
					default:
						_, _ = io.WriteString(conn, tag+" OK\r\n")
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestProxyAuthAndSplice(t *testing.T) {
	backendAddr, stop := fakeIMAPBackend(t)
	defer stop()

	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("proxy-password-ok")
	h := &connHandler{
		auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{
				accID: {ID: accID, OrganizationID: org, State: "connected"},
			}},
			Creds: memCredStore{accID: {Hash: hash}},
		},
		source: &memSource{t: &IMAPTarget{
			Addr: backendAddr, TLS: imapc.TLSNone, Username: "u", Password: "p",
		}},
		budget: newBudget(2), gate: alwaysOn{}, insecure: true,
	}

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
		h.serve(c)
	}()

	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	greet, err := br.ReadString('\n')
	if err != nil || !strings.Contains(greet, "OK") {
		t.Fatalf("greet %q %v", greet, err)
	}
	_, _ = io.WriteString(c, "A1 CAPABILITY\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "A1 OK") {
			break
		}
	}
	_, _ = io.WriteString(c, "A2 LOGIN "+accID+" proxy-password-ok\r\n")
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "A2 OK") {
		t.Fatalf("login %q %v", line, err)
	}
	_, _ = io.WriteString(c, "A3 LIST \"\" *\r\n")
	sawList := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "INBOX") {
			sawList = true
		}
		if strings.HasPrefix(line, "A3 OK") {
			break
		}
	}
	if !sawList {
		t.Fatal("LIST not proxied")
	}
	_, _ = io.WriteString(c, "A4 LOGOUT\r\n")
}

func TestParseLoginAndPlain(t *testing.T) {
	u, p, ok := parseLoginArgs(`user "pass word"`)
	if !ok || u != "user" || p != "pass word" {
		t.Fatalf("%q %q %v", u, p, ok)
	}
	u, p, ok = decodePlain("AHVzZXIAcGFzcw==") // \0user\0pass
	if !ok || u != "user" || p != "pass" {
		t.Fatalf("%q %q %v", u, p, ok)
	}
	tag, cmd, args := splitIMAP("A1 LOGIN x y")
	if tag != "A1" || cmd != "LOGIN" || args != "x y" {
		t.Fatalf("%s %s %s", tag, cmd, args)
	}
}

func TestQuoteIMAP(t *testing.T) {
	if quoteIMAP("plain") != "plain" {
		t.Fatal()
	}
	if quoteIMAP(`a"b`) != `"a\"b"` {
		t.Fatal(quoteIMAP(`a"b`))
	}
}

func TestDialBackendPlain(t *testing.T) {
	addr, stop := fakeIMAPBackend(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialBackend(ctx, &IMAPTarget{Addr: addr, TLS: imapc.TLSNone, Username: "u", Password: "p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}
