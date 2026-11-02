package imapproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

func testCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	cf, _ := os.Create(certFile)
	_ = pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = cf.Close()
	kb, _ := x509.MarshalECPrivateKey(key)
	kf, _ := os.Create(keyFile)
	_ = pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	_ = kf.Close()
	return certFile, keyFile
}

func TestServerStartTLSAddr(t *testing.T) {
	cert, key := testCertFiles(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	s := NewServer(Options{
		Enabled: true, AddrTLS: addr, TLSCertFile: cert, TLSKeyFile: key, AllowInsecureAuth: true,
	}, &connHandler{insecure: true, gate: alwaysOn{}, budget: newBudget(1),
		auth: &Authenticator{Accounts: &memAccounts{}, Creds: memCredStore{}},
	}, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestDialStartTLS(t *testing.T) {
	cert, key := testCertFiles(t)
	tlsCert, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
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
		defer func() { _ = c.Close() }()
		_, _ = c.Write([]byte("* OK\r\n"))
		buf := make([]byte, 256)
		n, _ := c.Read(buf)
		if !stringsContains(string(buf[:n]), "STARTTLS") {
			return
		}
		_, _ = c.Write([]byte("A001 OK\r\n"))
		tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{tlsCert}})
		if err := tc.Handshake(); err != nil {
			return
		}
		defer func() { _ = tc.Close() }()
		n, _ = tc.Read(buf)
		_ = n
		_, _ = tc.Write([]byte("A002 OK\r\n"))
		time.Sleep(30 * time.Millisecond)
	}()
	conn, err := dialBackend(context.Background(), &IMAPTarget{
		Addr: ln.Addr().String(), TLS: imapc.TLSStartTLS, InsecureSkipVerify: true,
		Username: "u", Password: "p",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestDialImplicitTLS(t *testing.T) {
	cert, key := testCertFiles(t)
	tlsCert, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{tlsCert}})
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
		_, _ = c.Write([]byte("* OK\r\n"))
		buf := make([]byte, 256)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("A002 OK\r\n"))
		time.Sleep(20 * time.Millisecond)
	}()
	conn, err := dialBackend(context.Background(), &IMAPTarget{
		Addr: ln.Addr().String(), TLS: imapc.TLSImplicit, InsecureSkipVerify: true,
		Username: "u", Password: "p",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}
