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

func TestServerListenAndProxy(t *testing.T) {
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
		source: &memSource{t: &IMAPTarget{Addr: backendAddr, TLS: imapc.TLSNone, Username: "u", Password: "p"}},
		budget: newBudget(2), gate: alwaysOn{}, insecure: true,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s := NewServer(Options{Enabled: true, Addr: addr, AllowInsecureAuth: true}, h, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c, "A LOGIN "+accID+" proxy-password-ok\r\n")
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "A OK") {
		t.Fatalf("%q %v", line, err)
	}
	_, _ = io.WriteString(c, "B LIST \"\" *\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "B OK") {
			break
		}
	}
}

func TestDialBackendXOAUTH2(t *testing.T) {
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
		_, _ = io.WriteString(c, "* OK\r\n")
		br := bufio.NewReader(c)
		line, _ := br.ReadString('\n')
		if !strings.Contains(line, "XOAUTH2") {
			t.Errorf("want xoauth2 got %q", line)
		}
		_, _ = io.WriteString(c, "A002 OK\r\n")
		time.Sleep(50 * time.Millisecond)
	}()
	conn, err := dialBackend(context.Background(), &IMAPTarget{
		Addr: ln.Addr().String(), TLS: imapc.TLSNone, Username: "u", AccessToken: "tok",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestDialBackendAuthFail(t *testing.T) {
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
		_, _ = io.WriteString(c, "* OK\r\n")
		br := bufio.NewReader(c)
		_, _ = br.ReadString('\n')
		_, _ = io.WriteString(c, "A002 NO bad\r\n")
	}()
	_, err = dialBackend(context.Background(), &IMAPTarget{
		Addr: ln.Addr().String(), TLS: imapc.TLSNone, Username: "u", Password: "p",
	}, nil)
	if err == nil {
		t.Fatal("expected auth fail")
	}
}

func TestServeBadCommand(t *testing.T) {
	h := &connHandler{insecure: true, gate: alwaysOn{}, budget: newBudget(1),
		auth: &Authenticator{Accounts: &memAccounts{}, Creds: memCredStore{}},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A SELECT INBOX\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "BAD") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, "B LOGIN\r\n") // missing args
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "BAD") {
		t.Fatal(line)
	}
}
