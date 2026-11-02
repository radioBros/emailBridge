package imapproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type alwaysOff struct{}

func (alwaysOff) IMAPProxyEnabled(context.Context) bool { return false }

func TestSessionCommands(t *testing.T) {
	h := &connHandler{insecure: true, gate: alwaysOn{}, budget: newBudget(2),
		auth: &Authenticator{Accounts: &memAccounts{byID: map[string]*AccountRef{}}, Creds: memCredStore{}},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A NOOP\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "OK") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, "B CAPABILITY\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "B OK") {
			break
		}
	}
	_, _ = io.WriteString(c2, "C LOGOUT\r\n")
	_, _ = br.ReadString('\n')
	_, _ = br.ReadString('\n')
}

func TestAuthPlainAndFail(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("proxy-password-ok")
	h := &connHandler{
		insecure: true, gate: alwaysOn{}, budget: newBudget(1),
		auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, State: "connected"}}},
			Creds:    memCredStore{accID: {Hash: hash}},
		},
		source: &memSource{t: nil}, // LoadIMAP returns nil → unavailable after auth
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	payload := base64.StdEncoding.EncodeToString([]byte("\x00" + accID + "\x00proxy-password-ok"))
	_, _ = io.WriteString(c2, "A AUTHENTICATE PLAIN\r\n")
	line, _ := br.ReadString('\n')
	if !strings.HasPrefix(line, "+") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, payload+"\r\n")
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "NO") {
		t.Fatalf("want unavailable, got %q", line)
	}
}

func TestGateOff(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	hash, _ := hashPassword("proxy-password-ok")
	h := &connHandler{
		insecure: true, gate: alwaysOff{}, budget: newBudget(1),
		auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: "o", State: "connected"}}},
			Creds:    memCredStore{accID: {Hash: hash}},
		},
		source: &memSource{},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A LOGIN "+accID+" proxy-password-ok\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "NO") {
		t.Fatal(line)
	}
}

func TestBudgetCap(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("proxy-password-ok")
	addr, stop := fakeIMAPBackend(t)
	defer stop()
	h := &connHandler{
		insecure: true, gate: alwaysOn{}, budget: newBudget(1),
		auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, State: "connected"}}},
			Creds:    memCredStore{accID: {Hash: hash}},
		},
		source: &memSource{t: &IMAPTarget{Addr: addr, TLS: imapc.TLSNone, Username: "u", Password: "p"}},
	}
	// hold the only slot
	if !h.budget.acquire(accID) {
		t.Fatal()
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A LOGIN "+accID+" proxy-password-ok\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "UNAVAILABLE") {
		t.Fatal(line)
	}
}

func TestServiceBadID(t *testing.T) {
	svc := &Service{Repo: &memRepo{}, OwnsAccount: ownsAll}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	if _, err := svc.Status(ctx, "bad"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("%v", err)
	}
}

func TestServerTLSRequired(t *testing.T) {
	s := NewServer(Options{Enabled: true, AddrTLS: "127.0.0.1:0"}, &connHandler{}, nil)
	if err := s.Start(); err == nil {
		t.Fatal("expected error")
	}
}

func TestServeSTARTTLSPath(t *testing.T) {
	cert, key := testCertFiles(t)
	tlsCert, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	h := &connHandler{
		insecure: false, gate: alwaysOn{}, budget: newBudget(1),
		tlsConf: &tls.Config{Certificates: []tls.Certificate{tlsCert}},
		auth:    &Authenticator{Accounts: &memAccounts{}, Creds: memCredStore{}},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A CAPABILITY\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "A OK") {
			break
		}
	}
	_, _ = io.WriteString(c2, "B LOGIN u p\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "PRIVACYREQUIRED") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, "C STARTTLS\r\n")
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "OK") {
		t.Fatal(line)
	}
	tc := tls.Client(c2, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	_ = tc.SetDeadline(time.Now().Add(2 * time.Second))
	br = bufio.NewReader(tc)
	_, _ = io.WriteString(tc, "D NOOP\r\n")
	line, err = br.ReadString('\n')
	if err != nil || !strings.Contains(line, "OK") {
		t.Fatalf("%q %v", line, err)
	}
}

func TestBufConnRead(t *testing.T) {
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	bc := &bufConn{Conn: c2, r: strings.NewReader("hello")}
	buf := make([]byte, 5)
	n, err := bc.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("%d %v %q", n, err, buf)
	}
}

func TestAuthenticateRequiresTLS(t *testing.T) {
	h := &connHandler{insecure: false, gate: alwaysOn{}, budget: newBudget(1),
		auth: &Authenticator{Accounts: &memAccounts{}, Creds: memCredStore{}},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A AUTHENTICATE PLAIN\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "PRIVACYREQUIRED") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, "B AUTHENTICATE LOGIN\r\n")
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "NO") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, " \r\n") // missing tag
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "BAD") {
		t.Fatal(line)
	}
}

func TestAuthCancelAndBackendDialFail(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("proxy-password-ok")
	h := &connHandler{
		insecure: true, gate: alwaysOn{}, budget: newBudget(2), log: nil,
		auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, OrganizationID: org, State: "connected"}}},
			Creds:    memCredStore{accID: {Hash: hash}},
		},
		source: &memSource{t: &IMAPTarget{Addr: "127.0.0.1:1", TLS: imapc.TLSNone, Username: "u", Password: "p"}},
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	go h.serve(c1)
	_ = c2.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c2)
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "A AUTHENTICATE PLAIN\r\n")
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c2, "*\r\n")
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "BAD") {
		t.Fatal(line)
	}
	_, _ = io.WriteString(c2, "B LOGIN "+accID+" proxy-password-ok\r\n")
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, "NO") {
		t.Fatal(line)
	}
}

func TestDecodePlainBad(t *testing.T) {
	if _, _, ok := decodePlain("!!!!"); ok {
		t.Fatal()
	}
	if _, _, ok := decodePlain(base64.StdEncoding.EncodeToString([]byte("only-one"))); ok {
		t.Fatal()
	}
}

func TestDisabledAccount(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	hash, _ := hashPassword("proxy-password-ok")
	a := &Authenticator{
		Accounts: &memAccounts{byID: map[string]*AccountRef{accID: {ID: accID, State: "deleting"}}},
		Creds:    memCredStore{accID: {Hash: hash}},
	}
	if _, err := a.Authenticate(context.Background(), accID, "proxy-password-ok", "1.1.1.1:1"); errs.CodeOf(err) != errs.CodeInvalidCredentials {
		t.Fatalf("%v", err)
	}
}
